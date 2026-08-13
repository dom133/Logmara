// Package alertengine evaluates alert rules against incoming syslog entries
// and dispatches notifications through the notify package when a rule's
// condition is met.
package alertengine

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"logmara/db"
	"logmara/model"
	"logmara/notify"
	"logmara/sharedstate"
)

// alertRulesReloadChannel carries a fire-and-forget "reload now" signal
// across replicas whenever one of them changes an alert rule, so every
// replica's cache picks up the change immediately instead of waiting for its
// own runReloadLoop tick (up to 30s later). The payload is unused - the
// message itself is the signal.
const alertRulesReloadChannel = "alerts:reload"

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
	pool        *db.DynamicPool
	store       counterStore
	dispatcher  *notify.Dispatcher
	broadcaster *sharedstate.Broadcaster // nil when Redis isn't configured

	mu          sync.RWMutex
	rulesByType map[string][]model.Alert
	reloadCh    chan struct{}
}

// NewEngine builds an Engine. redisClient may be nil (single-server
// deployments without Redis configured), in which case rule counters and
// cooldowns are tracked in-process instead of in Redis, and cache reloads
// stay local to this process.
//
// Active alert rules are loaded once here and cached in memory rather than
// re-queried from Postgres on every single EvaluateBatch/EvaluateMalformedJSON
// call - those run on the tailer's hot path (once per ingested batch, i.e.
// potentially dozens of times a second), and at that rate the rule lookup was
// itself a meaningful chunk of the per-batch DB round trips. The cache is
// refreshed when Reload is called (wired up to the alert CRUD handlers), by
// the periodic safety-net tick in runReloadLoop (also catches rule changes
// made directly in the database), and - when Redis is configured - the
// instant another replica's Reload publishes to alertRulesReloadChannel, so
// a rule change made through replica A takes effect on replica B without
// waiting for B's own next tick.
func NewEngine(ctx context.Context, pool *db.DynamicPool, redisClient *sharedstate.Client) *Engine {
	var store counterStore
	var broadcaster *sharedstate.Broadcaster
	if redisClient != nil {
		store = newRedisCounterStore(redisClient)
		broadcaster = sharedstate.NewBroadcaster(redisClient)
	} else {
		store = newLocalCounterStore()
	}
	e := &Engine{
		pool:        pool,
		store:       store,
		dispatcher:  notify.NewDispatcher(pool),
		broadcaster: broadcaster,
		reloadCh:    make(chan struct{}, 1),
	}
	e.loadRules()
	go e.runReloadLoop()
	if broadcaster != nil {
		go broadcaster.Subscribe(ctx, alertRulesReloadChannel, func(string) {
			e.reloadLocal()
		})
	}
	return e
}

// loadRules refreshes the in-memory active-rules cache from the database in
// a single query, grouped by rule_type.
func (e *Engine) loadRules() {
	alerts, err := db.GetAllActiveAlerts(e.pool.Get())
	if err != nil {
		slog.Error("failed to load alert rules", "error", err)
		return
	}

	byType := make(map[string][]model.Alert)
	for _, a := range alerts {
		byType[a.RuleType] = append(byType[a.RuleType], a)
	}

	e.mu.Lock()
	e.rulesByType = byType
	e.mu.Unlock()

	slog.Info("loaded active alert rules", "count", len(alerts))
}

func (e *Engine) runReloadLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			e.loadRules()
		case <-e.reloadCh:
			e.loadRules()
		}
	}
}

// Reload triggers an async refresh of the cached active alert rules on this
// replica, and - when Redis is configured - broadcasts the same signal to
// every other replica so they refresh too. Called after any create/update/
// delete/toggle of an alert so the change takes effect immediately instead
// of waiting for the next periodic tick.
func (e *Engine) Reload() {
	e.reloadLocal()
	if e.broadcaster != nil {
		if err := e.broadcaster.Publish(context.Background(), alertRulesReloadChannel, ""); err != nil {
			slog.Warn("failed to broadcast alert rules reload", "error", err)
		}
	}
}

// reloadLocal queues a refresh of this process's own cache only. Used by
// Reload and by the cross-replica subscriber alike, so a reload signal
// received from another replica triggers a local refresh without being
// re-published - publishing it again would ping-pong the same signal
// between replicas forever.
func (e *Engine) reloadLocal() {
	select {
	case e.reloadCh <- struct{}{}:
	default:
	}
}

// GetPool returns the pool the engine was built with, for handlers
// that need to run an alert CRUD query and then call Reload.
func (e *Engine) GetPool() *db.DynamicPool {
	return e.pool
}

// rulesOfType returns the cached active rules for ruleType (nil if none).
func (e *Engine) rulesOfType(ruleType string) []model.Alert {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rulesByType[ruleType]
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
func (e *Engine) EvaluateBatch(entries []model.IngestEntry) {
	if len(entries) == 0 {
		return
	}
	if db.GetSetting(e.pool.Get(), "notifications_enabled", "true") != "true" {
		return
	}

	rules := e.rulesOfType(model.RuleTypeLogThreshold)
	if len(rules) == 0 {
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
			if len(rule.FieldConditions) > 0 && !matchFieldConditions(rule.FieldConditions, decodeParsedFields(entry.ParsedFields), rule.FieldConditionsLogic) {
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
				_ = db.MarkAlertFired(e.pool.Get(), rule.ID)
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

		_ = db.MarkAlertFired(e.pool.Get(), rule.ID)
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
			// With OR logic only some conditions need to have matched -
			// only describe the ones entry actually satisfied, so the
			// history doesn't imply every condition applied.
			if rule.FieldConditionsLogic == model.FieldConditionsLogicOr && !evaluateFieldCondition(cond, fields[cond.FieldName]) {
				continue
			}
			lines = append(lines, fmt.Sprintf("Field %q %s %q (log value: %q)", cond.FieldName, cond.Operator, cond.Value, fields[cond.FieldName]))
		}
	}
	return lines
}

// EvaluateAuditLog fires any active audit_log alert rule whose
// AuditActionFilter matches (or is empty, meaning "any action"). Unlike
// log_threshold rules, audit log events have no count/window - each
// matching action fires immediately, subject only to the rule's cooldown.
func (e *Engine) EvaluateAuditLog(action string, userID int64, username, ip, details string) {
	if db.GetSetting(e.pool.Get(), "notifications_enabled", "true") != "true" {
		return
	}

	rules := e.rulesOfType(model.RuleTypeAuditLog)
	if len(rules) == 0 {
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

		_ = db.MarkAlertFired(e.pool.Get(), rule.ID)
		conditionDesc := "Any audit action (no filter set)"
		if rule.AuditActionFilter != "" {
			conditionDesc = fmt.Sprintf("Audit action matches: %s", rule.AuditActionFilter)
		}
		e.dispatcher.DispatchAlert(rule, notify.Payload{
			Title:             fmt.Sprintf("Audit log: %s", action),
			Message:           fmt.Sprintf("Action '%s' was performed. %s", action, details),
			Severity:          "warning",
			AuditLogRef:       &model.AuditLogRef{Timestamp: time.Now().UTC().Format(time.RFC3339), Action: action, Username: username, UserIP: ip, Details: details},
			MatchedConditions: []string{conditionDesc, fmt.Sprintf("Action performed: %s", action)},
		})
	}
}

// maxMalformedJSONSnippet caps how much of an offending raw line is embedded
// in the alert message - lines can be up to maxLineSize (10MB, see tailer.go)
// and there's no value in mailing/Slacking someone megabytes of garbage.
const maxMalformedJSONSnippet = 500

// EvaluateMalformedJSON fires any active malformed_json alert rule when the
// tailer encounters a syslog line that fails to unmarshal as JSON. This is
// admin-only by convention (see notify.Payload.AlertRuleType), same as
// audit_log and relay_cert_expiring. By default (FireOnEveryMatch unset)
// each occurrence fires immediately, subject only to the rule's cooldown -
// same as audit_log. With FireOnEveryMatch set, the cooldown is bypassed
// entirely and the rule notifies once per malformed line, no matter how
// close together they arrive.
func (e *Engine) EvaluateMalformedJSON(rawLine string) {
	if db.GetSetting(e.pool.Get(), "notifications_enabled", "true") != "true" {
		return
	}

	rules := e.rulesOfType(model.RuleTypeMalformedJSON)
	if len(rules) == 0 {
		return
	}

	snippet := rawLine
	if len(snippet) > maxMalformedJSONSnippet {
		snippet = snippet[:maxMalformedJSONSnippet] + "..."
	}

	for _, rule := range rules {
		if !rule.FireOnEveryMatch {
			key := fmt.Sprintf("%d", rule.ID)
			cooldown := time.Duration(rule.CooldownMinutes) * time.Minute
			if !e.store.shouldFire(key, 1, 1, time.Minute, cooldown) {
				continue
			}
		}

		_ = db.MarkAlertFired(e.pool.Get(), rule.ID)
		e.dispatcher.DispatchAlert(rule, notify.Payload{
			Title:             fmt.Sprintf("Alert: %s", rule.Name),
			Message:           fmt.Sprintf("A syslog line failed to parse as JSON during ingestion: %s", snippet),
			Severity:          "warning",
			MatchedConditions: []string{"Malformed JSON line encountered during ingestion"},
		})
	}
}
