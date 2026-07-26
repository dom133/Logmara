package sharedstate

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// RateCounter tracks how many events occurred per second, for coarse
// "events/sec" dashboard metrics - not for anything correctness-sensitive.
// Incr should be called once per event as it happens; Rate reports the
// average events/sec over the trailing windowSec seconds.
type RateCounter interface {
	Incr(n int)
	Rate(ctx context.Context, windowSec int) float64
}

// NewRateCounter returns a Redis-backed counter (shared across replicas, so
// every replica reports the same rate regardless of which one is actually
// producing the events - e.g. the tailer leader) when client is non-nil, or
// an in-memory one otherwise - the same fallback convention as the rest of
// this package. name namespaces the Redis keys (e.g. "lograte") so multiple
// counters can share one Redis instance.
func NewRateCounter(client *Client, name string) RateCounter {
	if client == nil {
		return newLocalRateCounter()
	}
	return newRedisRateCounter(client, name)
}

// ---- local: in-memory, single-process ----

const rateCounterBuckets = 60 // one slot per second, covers the last minute

type localRateCounter struct {
	mu      sync.Mutex
	counts  [rateCounterBuckets]int64
	seconds [rateCounterBuckets]int64 // unix second the slot was last written for
}

func newLocalRateCounter() *localRateCounter {
	return &localRateCounter{}
}

func (c *localRateCounter) Incr(n int) {
	now := time.Now().Unix()
	idx := now % rateCounterBuckets

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seconds[idx] != now {
		c.seconds[idx] = now
		c.counts[idx] = 0
	}
	c.counts[idx] += int64(n)
}

func (c *localRateCounter) Rate(_ context.Context, windowSec int) float64 {
	if windowSec <= 0 {
		return 0
	}
	now := time.Now().Unix()
	cutoff := now - int64(windowSec)

	c.mu.Lock()
	defer c.mu.Unlock()
	var total int64
	for i := 0; i < rateCounterBuckets; i++ {
		if c.seconds[i] > cutoff && c.seconds[i] <= now {
			total += c.counts[i]
		}
	}
	return float64(total) / float64(windowSec)
}

// ---- redis-backed: shared across replicas ----

// rateCounterTTL is a little over the largest window this type is ever asked
// to read (today: 10s), so a key always outlives every read that might still
// need it without lingering in Redis much past that.
const rateCounterTTL = 65 * time.Second

type redisRateCounter struct {
	client *Client
	prefix string
}

func newRedisRateCounter(client *Client, name string) *redisRateCounter {
	return &redisRateCounter{client: client, prefix: "rate:" + name + ":"}
}

func (c *redisRateCounter) Incr(n int) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rdb := c.client.Raw()

	key := c.prefix + strconv.FormatInt(time.Now().Unix(), 10)
	newCount, err := rdb.IncrBy(ctx, key, int64(n)).Result()
	if err != nil {
		slog.Warn("rate counter increment failed", "key", key, "error", err)
		return
	}
	if newCount == int64(n) {
		// This call created the key, so it owns setting its expiry.
		if err := rdb.Expire(ctx, key, rateCounterTTL).Err(); err != nil {
			slog.Warn("rate counter expire failed", "key", key, "error", err)
		}
	}
}

func (c *redisRateCounter) Rate(ctx context.Context, windowSec int) float64 {
	if windowSec <= 0 {
		return 0
	}
	rdb := c.client.Raw()

	now := time.Now().Unix()
	keys := make([]string, windowSec)
	for i := 0; i < windowSec; i++ {
		keys[i] = c.prefix + strconv.FormatInt(now-int64(i), 10)
	}

	vals, err := rdb.MGet(ctx, keys...).Result()
	if err != nil {
		slog.Warn("rate counter read failed", "prefix", c.prefix, "error", err)
		return 0
	}

	var total int64
	for _, v := range vals {
		s, ok := v.(string)
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			continue
		}
		total += n
	}
	return float64(total) / float64(windowSec)
}
