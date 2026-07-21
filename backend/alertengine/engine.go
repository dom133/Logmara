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
		var matchedEntries []model.IngestEntry
		for _, entry := range entries {
			if !meetsSeverity(entry.Severity, rule.Severity) {
				continue
			}
			if !matchDevice(rule.DeviceIPs, entry.FromHostIP) {
				continue
			}
			if !matchParsers(rule.ParserNames, entry.MatchedParsers) {
				continue
			}
			if !matchPattern(rule.MessagePattern, entry.Message) {
				continue
			}
			if len(rule.FieldConditions) > 0 && !matchFieldConditions(rule.FieldConditions, decodeParsedFields(entry.ParsedFields)) {
				continue
			}
			matchedEntries = append(matchedEntries, entry)
		}
		if len(matchedEntries) == 0 {
			continue
		}

		if rule.FireOnEveryMatch {
			// Every matching entry notifies on its own, bypassing the
			// threshold/window/cooldown gate entirely.
			for _, entry := range matchedEntries {
				_ = db.MarkAlertFired(database, rule.ID)
				conditions := describeMatchedConditions(rule, entry)
				e.dispatcher.DispatchAlert(rule, notify.Payload{
					Title:             fmt.Sprintf("Alert: %s", rule.Name),
					Message:           fmt.Sprintf("Matching log entry from %s: %s", entry.Hostname, entry.Message),
					Severity:          notifySeverity(entry.Severity),
					TriggerLog:        triggerLogSnapshot(entry),
					MatchedConditions: conditions,
				})
			}
			continue
		}

		lastEntry := matchedEntries[len(matchedEntries)-1]
		matched := len(matchedEntries)

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
		conditions := describeMatchedConditions(rule, lastEntry)
		conditions = append(conditions, fmt.Sprintf("Threshold reached: %d matching log(s) within %d minute(s) (required: %d)", matched, rule.WindowMinutes, threshold))
		e.dispatcher.DispatchAlert(rule, notify.Payload{
			Title: fmt.Sprintf("Alert: %s", rule.Name),
			Message: fmt.Sprintf("%d matching log entries in the last %d minute(s). Latest from %s: %s",
				matched, rule.WindowMinutes, lastEntry.Hostname, lastEntry.Message),
			Severity:          notifySeverity(lastEntry.Severity),
			TriggerLog:        triggerLogSnapshot(lastEntry),
			MatchedConditions: conditions,
		})
	}
}

// triggerLogSnapshot copies the fields of the log entry that triggered an
// alert into a self-contained record, so it survives independently of
// syslog_logs retention cleanup (see model.TriggerLogSnapshot).
func triggerLogSnapshot(entry model.IngestEntry) *model.TriggerLogSnapshot {
	return &model.TriggerLogSnapshot{
		Timestamp:  entry.Timestamp,
		Hostname:   entry.Hostname,
		FromHostIP: entry.FromHostIP,
		AppName:    entry.AppName,
		Severity:   entry.Severity,
		Message:    entry.Message,
	}
}

// describeMatchedConditions lists which of the rule's filters entry
// satisfied - entry is assumed to already pass every one of them (only
// ever called with the representative entry from EvaluateBatch's matching
// loop, which only advances lastEntry once every filter has passed). An
// unrestricted rule (no severity/device/parser/pattern/field filters)
// yields an empty list, since there was nothing besides the threshold to
// satisfy.
func describeMatchedConditions(rule model.Alert, entry model.IngestEntry) []string {
	var lines []string
	if rule.Severity != "" {
		lines = append(lines, fmt.Sprintf("Severity %s or more severe (log severity: %s)", rule.Severity, entry.Severity))
	}
	if len(rule.DeviceIPs) > 0 {
		lines = append(lines, fmt.Sprintf("Device is one of: %s (log device: %s)", strings.Join(rule.DeviceIPs, ", "), entry.FromHostIP))
	}
	if len(rule.ParserNames) > 0 {
		lines = append(lines, fmt.Sprintf("Parser matches one of: %s (log parsers: %s)", strings.Join(rule.ParserNames, ", "), strings.Join(entry.MatchedParsers, ", ")))
	}
	if rule.MessagePattern != "" {
		lines = append(lines, fmt.Sprintf("Message matches pattern: %q", rule.MessagePattern))
	}
	if len(rule.FieldConditions) > 0 {
		fields := decodeParsedFields(entry.ParsedFields)
		for _, cond := range rule.FieldConditions {
			lines = append(lines, fmt.Sprintf("Field %q %s %q (log value: %q)", cond.FieldName, cond.Operator, cond.Value, fields[cond.FieldName]))
		}
	}
	return lines
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
		conditionDesc := "Any audit action (no filter set)"
		if rule.AuditActionFilter != "" {
			conditionDesc = fmt.Sprintf("Audit action matches: %s", rule.AuditActionFilter)
		}
		e.dispatcher.DispatchAlert(rule, notify.Payload{
			Title:             fmt.Sprintf("Config change: %s", action),
			Message:           fmt.Sprintf("Action '%s' was performed. %s", action, details),
			Severity:          "warning",
			MatchedConditions: []string{conditionDesc, fmt.Sprintf("Action performed: %s", action)},
		})
	}
}
