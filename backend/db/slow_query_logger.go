package db

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

const SlowQueryThreshold = 500 * time.Millisecond

type SlowQueryLogger struct {
	db        *sql.DB
	name      string
	threshold time.Duration
}

func NewSlowQueryLogger(db *sql.DB, name string) *SlowQueryLogger {
	return &SlowQueryLogger{
		db:        db,
		name:      name,
		threshold: SlowQueryThreshold,
	}
}

func (l *SlowQueryLogger) Query(query string, args ...interface{}) (*sql.Rows, error) {
	start := time.Now()
	rows, err := l.db.Query(query, args...)
	elapsed := time.Since(start)
	if elapsed > l.threshold {
		slog.Warn("slow query", "name", l.name, "duration_ms", elapsed.Milliseconds(), "query_preview", truncate(query, 120))
	}
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (l *SlowQueryLogger) QueryRow(query string, args ...interface{}) *sql.Row {
	return l.db.QueryRow(query, args...)
}

func (l *SlowQueryLogger) Exec(query string, args ...interface{}) (sql.Result, error) {
	start := time.Now()
	result, err := l.db.Exec(query, args...)
	elapsed := time.Since(start)
	if elapsed > l.threshold {
		slog.Warn("slow exec", "name", l.name, "duration_ms", elapsed.Milliseconds(), "query_preview", truncate(query, 120))
	}
	return result, err
}

func (l *SlowQueryLogger) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	start := time.Now()
	rows, err := l.db.QueryContext(ctx, query, args...)
	elapsed := time.Since(start)
	if elapsed > l.threshold {
		slog.Warn("slow query ctx", "name", l.name, "duration_ms", elapsed.Milliseconds(), "query_preview", truncate(query, 120))
	}
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
