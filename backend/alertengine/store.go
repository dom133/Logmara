package alertengine

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"logmara/sharedstate"
)

// counterStore tracks, per key, how many matching events have been seen
// within the current window and whether that key is still in its post-fire
// cooldown. shouldFire atomically increments the counter by delta and
// returns true exactly once per cooldown period, the moment the running
// count first reaches threshold. key is normally an alert rule ID, but
// device_silence rules further scope it per device (see silence.go), since
// one rule can watch several devices that go silent independently.
type counterStore interface {
	shouldFire(key string, delta, threshold int, window, cooldown time.Duration) bool
}

// ---- local: in-memory, single-process ----

type localCounter struct {
	count        int
	windowEnds   time.Time
	cooldownEnds time.Time
}

type localCounterStore struct {
	mu     sync.Mutex
	counts map[string]*localCounter
}

func newLocalCounterStore() *localCounterStore {
	return &localCounterStore{counts: make(map[string]*localCounter)}
}

func (s *localCounterStore) shouldFire(key string, delta, threshold int, window, cooldown time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	c, ok := s.counts[key]
	if !ok || now.After(c.windowEnds) {
		c = &localCounter{windowEnds: now.Add(window)}
		s.counts[key] = c
	}
	c.count += delta

	if c.count < threshold {
		return false
	}
	if now.Before(c.cooldownEnds) {
		return false
	}
	c.cooldownEnds = now.Add(cooldown)
	c.count = 0
	return true
}

// ---- redis-backed: shared across replicas ----

type redisCounterStore struct {
	client *sharedstate.Client
}

func newRedisCounterStore(client *sharedstate.Client) *redisCounterStore {
	return &redisCounterStore{client: client}
}

func (s *redisCounterStore) shouldFire(key string, delta, threshold int, window, cooldown time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rdb := s.client.Raw()

	countKey := "alertcount:" + key
	newCount, err := rdb.IncrBy(ctx, countKey, int64(delta)).Result()
	if err != nil {
		slog.Warn("alert counter increment failed", "key", key, "error", err)
		return false
	}
	if newCount == int64(delta) {
		// This call created the key, so it owns starting its window.
		if err := rdb.Expire(ctx, countKey, window).Err(); err != nil {
			slog.Warn("alert counter expire failed", "key", key, "error", err)
		}
	}
	if newCount < int64(threshold) {
		return false
	}

	cooldownKey := "alertcooldown:" + key
	acquired, err := rdb.SetNX(ctx, cooldownKey, "1", cooldown).Result()
	if err != nil {
		slog.Warn("alert cooldown check failed", "key", key, "error", err)
		return false
	}
	if !acquired {
		return false
	}

	rdb.Del(ctx, countKey)
	return true
}
