// Package audit centralizes writes to the audit_log table so that every
// audited action can also be checked against active config_change alert
// rules in one place, rather than wiring that check into each handler.
package audit

import (
	"database/sql"
	"log/slog"
	"sync/atomic"

	"syslog-gui/alertengine"
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
