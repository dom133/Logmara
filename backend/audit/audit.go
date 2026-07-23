// Package audit centralizes writes to the audit_log table so that every
// audited action can also be checked against active config_change alert
// rules in one place, rather than wiring that check into each handler.
package audit

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sync/atomic"

	"syslytics/alertengine"
)

var engine atomic.Pointer[alertengine.Engine]

// SetAlertEngine registers the alert engine LogAudit hands audited actions
// to. Call once at startup; leaving it unset (e.g. the setup wizard's
// minimal, database-less server) just skips alert evaluation.
func SetAlertEngine(e *alertengine.Engine) {
	engine.Store(e)
}

// LogAudit records an entry in audit_log and, if a config_change alert rule
// matches action, fires it. Failures are logged, not returned - audit
// logging must never block or fail the request that triggered it.
func LogAudit(db *sql.DB, userID int64, username, action, ip, details string) {
	_, err := db.Exec(
		"INSERT INTO audit_log (user_id, username, action, ip, details, created_at) VALUES ($1, $2, $3, $4, $5, NOW())",
		userID, username, action, ip, details,
	)
	if err != nil {
		slog.Error("audit log error", "error", err)
		return
	}

	if e := engine.Load(); e != nil {
		e.EvaluateConfigChange(db, action, details)
	}
}

// LogSlowQuery logs slow queries to audit_log with action="slow_query" and
// details containing the duration and query text.
func LogSlowQuery(db *sql.DB, durationMs int, query string) {
	details := fmt.Sprintf("duration_ms=%d, query=%s", durationMs, query)
	LogAudit(db, 0, "", "slow_query", "", details)
}

// LogBulkOperation logs bulk operations like bulk delete/export with
// action="bulk_operation" and details containing operation name and count.
func LogBulkOperation(db *sql.DB, userID int64, username, operation string, count int) {
	details := fmt.Sprintf("operation=%s, count=%d", operation, count)
	LogAudit(db, userID, username, "bulk_operation", "", details)
}
