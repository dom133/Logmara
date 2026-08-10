package db

import (
	"database/sql"
	"log/slog"
	"sync/atomic"
)

// DynamicPool wraps a *sql.DB behind an atomic.Value so the underlying
// connection pool can be swapped when Vault rotates the PostgreSQL
// credentials. Callers use Get() to retrieve the current *sql.DB.
type DynamicPool struct {
	db atomic.Value // *sql.DB
}

// NewDynamicPool creates a DynamicPool with the given DSN.
func NewDynamicPool(dsn string) (*DynamicPool, error) {
	database, err := Connect(dsn)
	if err != nil {
		return nil, err
	}
	dp := &DynamicPool{}
	dp.db.Store(database)
	return dp, nil
}

// Get returns the current *sql.DB. Safe to call concurrently.
func (dp *DynamicPool) Get() *sql.DB {
	return dp.db.Load().(*sql.DB)
}

// Rotate closes the current pool, opens a new one with the updated DSN,
// and atomically swaps it in. Old connections expire via SetConnMaxLifetime
// (default 30m), so in-flight queries complete gracefully.
func (dp *DynamicPool) Rotate(dsn string) error {
	oldDB := dp.db.Load().(*sql.DB)

	newDB, err := Connect(dsn)
	if err != nil {
		slog.Warn("db: rotation failed, keeping existing pool", "error", err)
		return err
	}

	oldDB.Close()
	dp.db.Store(newDB)
	slog.Info("db: pool rotated successfully")
	return nil
}

// Close shuts down the current pool.
func (dp *DynamicPool) Close() {
	db := dp.db.Load().(*sql.DB)
	db.Close()
}
