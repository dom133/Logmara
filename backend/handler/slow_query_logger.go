package handler

import (
	"sync"
	"time"
)

type SlowQueryRecord struct {
	Name     string `json:"name"`
	Duration int64  `json:"duration_ms"`
	TS       string `json:"timestamp"`
}

var (
	slowQueries   []SlowQueryRecord
	slowQueriesMu sync.RWMutex
)

const maxSlowQueries = 1000

func recordSlowQuery(name string, duration time.Duration) {
	slowQueriesMu.Lock()
	defer slowQueriesMu.Unlock()
	if len(slowQueries) >= maxSlowQueries {
		slowQueries = slowQueries[1:]
	}
	slowQueries = append(slowQueries, SlowQueryRecord{
		Name:     name,
		Duration: duration.Milliseconds(),
		TS:       time.Now().Format(time.RFC3339),
	})
}

func GetSlowQueryRecords() []SlowQueryRecord {
	slowQueriesMu.RLock()
	defer slowQueriesMu.RUnlock()
	out := make([]SlowQueryRecord, len(slowQueries))
	copy(out, slowQueries)
	return out
}

func ClearSlowQueries() {
	slowQueriesMu.Lock()
	defer slowQueriesMu.Unlock()
	slowQueries = nil
}