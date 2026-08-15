// Package alertengine evaluates alert rules against incoming syslog entries
// and dispatches notifications through the notify package when a rule's
// condition is met.
package alertengine

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"syslog-gui/db"
	"syslog-gui/model"
	"syslog-gui/notify"
	"syslog-gui/sharedstate"
)

var severityRank = map[string]int{
	"emerg": 0, "emergency": 0,
	"alert": 1,
	"crit":  2, "critical": 2,
	"err": 3, "error": 3,
	"warning": 4, "warn": 4,
	"notice": 5,
	"info":   6,
	"debug":  7,
}

// meetsSeverity reports whether entrySeverity is at least as severe as
// minSeverity (lower rank = more severe, following syslog convention). An
// empty minSeverity or an unrecognized value on either side always matches.
func meetsSeverity(entrySeverity, minSeverity string) bool {
	if minSeverity == "" {
		return true
	}
	er, ok1 := severityRank[strings.ToLower(entrySeverity)]
	mr, ok2 := severityRank[strings.ToLower(minSeverity)]
	if !ok1 || !ok2 {
		return true
	}
	return er <= mr
}

// notifySeverity buckets a syslog severity into the coarser info/warning/
// error/critical scale used for notification payloads.
func notifySeverity(s string) string {
	switch severityRank[strings.ToLower(s)] {
	case 0, 1, 2:
		return "critical"
	case 3:
		return "error"
	case 4:
		return "warning"
	default:
		return "info"
	}
}

// matchPattern matches value against pattern: empty matches everything, a
// pattern containing "*" is treated as a glob, otherwise it's a case
// insensitive substring match.
func matchPattern(pattern, value string) bool {
	if pattern == "" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return strings.Contains(strings.ToLower(value), strings.ToLower(pattern))
	}
	re, err := globToRegex(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

func globToRegex(pattern string) (*regexp.Regexp, error) {
	parts := strings.Split(pattern, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.Compile("(?is)^" + strings.Join(parts, ".*") + "$")
}

// Engine evaluates active alert rules against ingested log batches. It is
// safe for concurrent use, though in practice EvaluateBatch is only ever
// called from the single tailer goroutine that currently holds ingestion
// leadership.
type Engine struct {
	store      counterStore
	dispatcher *notify.Dispatcher
}

// NewEngine builds an Engine. redisClient may be nil (single-server
// deployments without Redis configured), in which case rule counters and
// cooldowns are tracked in-process instead of in Redis.
func NewEngine(database *sql.DB, redisClient *sharedstate.Client) *Engine {
	var store counterStore
	if redisClient != nil {
		store = newRedisCounterStore(redisClient)
	} else {
		store = newLocalCounterStore()
	}
	return &Engine{store: store, dispatcher: notify.NewDispatcher(database)}
}

// SetOnInApp registers a callback invoked whenever an in_app channel fires,
// so the caller (main.go, wiring up notifyhub) can fan the notification out
// to connected clients over SSE.
func (e *Engine) SetOnInApp(fn func(model.InAppNotification)) {
	e.dispatcher.OnInApp = fn
}

// EvaluateBatch checks every active log_threshold alert rule against the
// entries just flushed to the database, firing (and logging) a notification
// for any rule whose threshold is met within its window and whose cooldown
// has elapsed.
func (e *Engine) EvaluateBatch(database *sql.DB, entries []model.IngestEntry) {
	if len(entries) == 0 {
		return
	}
	if db.GetSetting(database, "notifications_enabled", "true") != "true" {
		return
	}

	rules, err := db.GetActiveAlertsByType(database, model.RuleTypeLogThreshold)
	if err != nil || len(rules) == 0 {
		return
	}

	for _, rule := range rules {
		matched := 0
		var lastEntry model.IngestEntry
		for _, entry := range entries {
			if !meetsSeverity(entry.Severity, rule.Severity) {
				continue
			}
			if !matchPattern(rule.HostnamePattern, entry.Hostname) {
				continue
			}
			if !matchPattern(rule.AppNamePattern, entry.AppName) {
				continue
			}
			if !matchPattern(rule.MessagePattern, entry.Message) {
				continue
			}
			matched++
			lastEntry = entry
		}
		if matched == 0 {
			continue
		}

		threshold := rule.Threshold
		if threshold <= 0 {
			threshold = 1
		}
		window := time.Duration(rule.WindowMinutes) * time.Minute
		cooldown := time.Duration(rule.CooldownMinutes) * time.Minute

		key := fmt.Sprintf("%d", rule.ID)
		if !e.store.shouldFire(key, matched, threshold, window, cooldown) {
			continue
		}

		_ = db.MarkAlertFired(database, rule.ID)
		e.dispatcher.DispatchAlert(rule, notify.Payload{
			Title: fmt.Sprintf("Alert: %s", rule.Name),
			Message: fmt.Sprintf("%d matching log entries in the last %d minute(s). Latest from %s: %s",
				matched, rule.WindowMinutes, lastEntry.Hostname, lastEntry.Message),
			Severity: notifySeverity(lastEntry.Severity),
		})
	}
}

// EvaluateConfigChange fires any active config_change alert rule whose
// AuditActionFilter matches (or is empty, meaning "any action"). Unlike
// log_threshold rules, config changes have no count/window - each matching
// action fires immediately, subject only to the rule's cooldown.
func (e *Engine) EvaluateConfigChange(database *sql.DB, action, details string) {
	if db.GetSetting(database, "notifications_enabled", "true") != "true" {
		return
	}

	rules, err := db.GetActiveAlertsByType(database, model.RuleTypeConfigChange)
	if err != nil || len(rules) == 0 {
		return
	}

	for _, rule := range rules {
		if rule.AuditActionFilter != "" && rule.AuditActionFilter != action {
			continue
		}

		key := fmt.Sprintf("%d", rule.ID)
		cooldown := time.Duration(rule.CooldownMinutes) * time.Minute
		if !e.store.shouldFire(key, 1, 1, time.Minute, cooldown) {
			continue
		}

		_ = db.MarkAlertFired(database, rule.ID)
		e.dispatcher.DispatchAlert(rule, notify.Payload{
			Title:    fmt.Sprintf("Config change: %s", action),
			Message:  fmt.Sprintf("Action '%s' was performed. %s", action, details),
			Severity: "warning",
		})
	}
}
