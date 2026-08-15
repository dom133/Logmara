package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"log/slog"
	"time"

	"github.com/lib/pq"
)

const slowQueryThreshold = 500 * time.Millisecond

// slowQueryHook lets the handler package's admin slow-query log (and its
// Redis-backed variant) see queries recorded at the driver level too, not
// just the ones wrapped in handler.timedQuery. db can't import handler
// (handler already imports db), so main.go wires this up with
// db.SetSlowQueryHook(handler.RecordSlowQuery) at startup instead.
var slowQueryHook func(name string, duration time.Duration)

func SetSlowQueryHook(fn func(name string, duration time.Duration)) {
	slowQueryHook = fn
}

func reportSlowQuery(query string, elapsed time.Duration) {
	if elapsed < slowQueryThreshold {
		return
	}
	preview := query
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}
	slog.Warn("slow db query", "duration_ms", elapsed.Milliseconds(), "query", preview)
	if slowQueryHook != nil {
		slowQueryHook(preview, elapsed)
	}
}

// init registers a driver name that wraps lib/pq so every query run through
// the resulting *sql.DB - Query, Exec, QueryRow, the Context variants, and
// anything run inside a transaction or a prepared statement - is timed and
// logged when it crosses slowQueryThreshold, without touching any of the
// dozens of call sites across the handler/db packages.
func init() {
	sql.Register("postgres-instrumented", instrumentedDriver{})
}

type instrumentedDriver struct{}

func (instrumentedDriver) Open(name string) (driver.Conn, error) {
	c, err := (&pq.Driver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &instrumentedConn{Conn: c}, nil
}

// instrumentedConn wraps a pq connection. It only forwards to the optional
// driver interfaces pq itself implements (checked via type assertion below)
// so wrapping doesn't add or remove any capability the underlying driver
// has - context-aware query/exec, transactions, prepared statements,
// pooling's session reset/liveness checks, and Ping all keep working
// exactly as they did unwrapped.
type instrumentedConn struct {
	driver.Conn
}

func (c *instrumentedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	rows, err := q.QueryContext(ctx, query, args)
	reportSlowQuery(query, time.Since(start))
	return rows, err
}

func (c *instrumentedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	e, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	res, err := e.ExecContext(ctx, query, args)
	reportSlowQuery(query, time.Since(start))
	return res, err
}

func (c *instrumentedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var stmt driver.Stmt
	var err error
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err = p.PrepareContext(ctx, query)
	} else {
		stmt, err = c.Conn.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return &instrumentedStmt{Stmt: stmt, query: query}, nil
}

func (c *instrumentedConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &instrumentedStmt{Stmt: stmt, query: query}, nil
}

func (c *instrumentedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

func (c *instrumentedConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *instrumentedConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (c *instrumentedConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

// instrumentedStmt wraps a prepared statement so db.Prepare/tx.Prepare
// callers (bulk inserts in tailer, parser, alert engine) are timed too, the
// same as ad-hoc queries run directly on *sql.DB.
type instrumentedStmt struct {
	driver.Stmt
	query string
}

func (s *instrumentedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	e, ok := s.Stmt.(driver.StmtExecContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	res, err := e.ExecContext(ctx, args)
	reportSlowQuery(s.query, time.Since(start))
	return res, err
}

func (s *instrumentedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	rows, err := q.QueryContext(ctx, args)
	reportSlowQuery(s.query, time.Since(start))
	return rows, err
}

func (s *instrumentedStmt) Exec(args []driver.Value) (driver.Result, error) {
	start := time.Now()
	res, err := s.Stmt.Exec(args)
	reportSlowQuery(s.query, time.Since(start))
	return res, err
}

func (s *instrumentedStmt) Query(args []driver.Value) (driver.Rows, error) {
	start := time.Now()
	rows, err := s.Stmt.Query(args)
	reportSlowQuery(s.query, time.Since(start))
	return rows, err
}
