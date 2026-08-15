package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"syslog-gui/sharedstate"
)

type SlowQueryRecord struct {
	Name     string `json:"name"`
	Duration int64  `json:"duration_ms"`
	TS       string `json:"timestamp"`
}

const maxSlowQueries = 1000

type slowQueryStore interface {
	Record(rec SlowQueryRecord)
	List() []SlowQueryRecord
	Clear()
}

// currentSlowQueryStore defaults to the local, in-memory implementation
// (single-server / Redis not configured) - today's exact behavior.
// SetSlowQueryStore swaps it for a Redis-backed one when Redis is
// configured, so GET/DELETE /admin/slow-queries see the same data
// regardless of which api replica answers the request.
var currentSlowQueryStore slowQueryStore = newLocalSlowQueryStore()

func SetSlowQueryStore(client *sharedstate.Client) {
	if client == nil {
		return
	}
	currentSlowQueryStore = newRedisSlowQueryStore(client)
}

func recordSlowQuery(name string, duration time.Duration) {
	currentSlowQueryStore.Record(SlowQueryRecord{
		Name:     name,
		Duration: duration.Milliseconds(),
		TS:       time.Now().Format(time.RFC3339),
	})
}

func GetSlowQueryRecords() []SlowQueryRecord {
	return currentSlowQueryStore.List()
}

func ClearSlowQueries() {
	currentSlowQueryStore.Clear()
}

// ---- local: in-memory, single-process (original implementation) ----

type localSlowQueryStore struct {
	mu      sync.RWMutex
	entries []SlowQueryRecord
}

func newLocalSlowQueryStore() *localSlowQueryStore {
	return &localSlowQueryStore{}
}

func (s *localSlowQueryStore) Record(rec SlowQueryRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) >= maxSlowQueries {
		s.entries = s.entries[1:]
	}
	s.entries = append(s.entries, rec)
}

func (s *localSlowQueryStore) List() []SlowQueryRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SlowQueryRecord, len(s.entries))
	copy(out, s.entries)
	return out
}

func (s *localSlowQueryStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
}

// ---- redis-backed: shared across replicas ----

const slowQueryListKey = "slowqueries"

type redisSlowQueryStore struct {
	client *sharedstate.Client
}

func newRedisSlowQueryStore(client *sharedstate.Client) *redisSlowQueryStore {
	return &redisSlowQueryStore{client: client}
}

func (s *redisSlowQueryStore) Record(rec SlowQueryRecord) {
	data, err := json.Marshal(rec)
	if err != nil {
		slog.Warn("slow query marshal error", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// RPUSH (append at tail) + LTRIM to the last maxSlowQueries elements
	// mirrors the local store's "append, drop oldest from the front once
	// over cap" semantics, keeping List() in the same oldest-to-newest
	// order either implementation produces.
	pipe := s.client.Raw().TxPipeline()
	pipe.RPush(ctx, slowQueryListKey, data)
	pipe.LTrim(ctx, slowQueryListKey, -maxSlowQueries, -1)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("slow query record error", "error", err)
	}
}

func (s *redisSlowQueryStore) List() []SlowQueryRecord {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	raw, err := s.client.Raw().LRange(ctx, slowQueryListKey, 0, -1).Result()
	if err != nil {
		slog.Warn("slow query list error", "error", err)
		return nil
	}
	out := make([]SlowQueryRecord, 0, len(raw))
	for _, r := range raw {
		var rec SlowQueryRecord
		if err := json.Unmarshal([]byte(r), &rec); err == nil {
			out = append(out, rec)
		}
	}
	return out
}

func (s *redisSlowQueryStore) Clear() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.client.Raw().Del(ctx, slowQueryListKey).Err(); err != nil {
		slog.Warn("slow query clear error", "error", err)
	}
}
