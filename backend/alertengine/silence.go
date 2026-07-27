package alertengine

import (
	"database/sql"
	"fmt"
	"time"

	"logmara/db"
	"logmara/model"
	"logmara/notify"
)

type deviceLastSeen struct {
	Hostname   string
	FromHostIP string
	LastSeen   time.Time
}

// loadDeviceLastSeen reads per-device last-seen timestamps from
// mv_device_stats - the same materialized view the dashboard uses - rather
// than scanning syslog_logs directly, which would mean an unindexed
// MAX(timestamp) GROUP BY hostname across every partition on each check.
// This means silence detection lags real time by up to
// mv_refresh_interval_min (30 minutes by default), same as "last seen"
// already does everywhere else in the app.
func loadDeviceLastSeen(database *sql.DB) ([]deviceLastSeen, error) {
	rows, err := database.Query("SELECT fromhost_ip, hostname, last_seen FROM mv_device_stats WHERE last_seen IS NOT NULL")
	if err != nil {
		return nil, fmt.Errorf("load device last-seen: %w", err)
	}
	defer rows.Close()

	var devices []deviceLastSeen
	for rows.Next() {
		var d deviceLastSeen
		if err := rows.Scan(&d.FromHostIP, &d.Hostname, &d.LastSeen); err != nil {
			return nil, fmt.Errorf("scan device last-seen: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, nil
}

// silenceSeverityRank orders severities so escalation can be detected (only
// firing immediately, bypassing cooldown, when it strictly increases).
var silenceSeverityRank = map[string]int{"warning": 0, "error": 1, "critical": 2}

// severityForSilence escalates severity the longer a device stays silent
// relative to its rule's threshold: still just past threshold is a
// "warning", past 2x is "error", past 4x is "critical" - so a device that's
// been down for hours doesn't look the same as one that missed one check.
func severityForSilence(silentFor, threshold time.Duration) string {
	switch {
	case silentFor >= 4*threshold:
		return "critical"
	case silentFor >= 2*threshold:
		return "error"
	default:
		return "warning"
	}
}

// CheckDeviceSilence fires any active device_silence rule whose matching
// device(s) haven't logged anything in over Threshold minutes, escalating
// severity the longer they stay silent, and sends a "back online" notice
// the first check after a previously-silent device resumes logging.
// Intended to be called periodically (see the ticker in main.go),
// independently on every api replica - safe because the per-rule-per-device
// cooldown key dedupes duplicate fires the same way log_threshold rules do,
// and the device_silence_state table (not per-replica memory) is what
// tracks whether a device is already known to be silent, so replicas agree.
func (e *Engine) CheckDeviceSilence(database *sql.DB) {
	if db.GetSetting(database, "notifications_enabled", "true") != "true" {
		return
	}

	rules := e.rulesOfType(model.RuleTypeDeviceSilence)
	if len(rules) == 0 {
		return
	}

	devices, err := loadDeviceLastSeen(database)
	if err != nil || len(devices) == 0 {
		return
	}

	states, err := db.ListDeviceSilenceStates(database)
	if err != nil {
		states = map[string]db.SilenceState{}
	}

	now := time.Now()
	for _, rule := range rules {
		silentAfterMin := rule.Threshold
		if silentAfterMin <= 0 {
			silentAfterMin = 15
		}
		threshold := time.Duration(silentAfterMin) * time.Minute
		cooldown := time.Duration(rule.CooldownMinutes) * time.Minute

		for _, dev := range devices {
			if !matchDevice(rule.DeviceIPs, dev.FromHostIP) {
				continue
			}

			stateKey := fmt.Sprintf("%d:%s", rule.ID, dev.FromHostIP)
			silentFor := now.Sub(dev.LastSeen)
			state, hadState := states[stateKey]

			if silentFor < threshold {
				if hadState {
					e.dispatcher.DispatchAlert(rule, notify.Payload{
						Title: fmt.Sprintf("Device back online: %s", dev.Hostname),
						Message: fmt.Sprintf("%s (%s) has resumed sending logs (was silent since %s).",
							dev.Hostname, dev.FromHostIP, state.SilentSince.Format(time.RFC3339)),
						Severity: "info",
					})
					_ = db.DeleteDeviceSilenceState(database, rule.ID, dev.FromHostIP)
				}
				continue
			}

			since := now.Add(-silentFor)
			if hadState {
				since = state.SilentSince
			}
			severity := severityForSilence(now.Sub(since), threshold)

			// Always call shouldFire (even when escalation will fire
			// regardless) so its cooldown bookkeeping stays correct for
			// subsequent, non-escalated ticks.
			storeFire := e.store.shouldFire(stateKey, 1, 1, time.Minute, cooldown)
			escalated := hadState && silenceSeverityRank[severity] > silenceSeverityRank[state.LastSeverity]
			if !escalated && !storeFire {
				continue
			}

			_ = db.MarkAlertFired(database, rule.ID)
			_ = db.UpsertDeviceSilenceState(database, rule.ID, dev.FromHostIP, since, severity)
			e.dispatcher.DispatchAlert(rule, notify.Payload{
				Title: fmt.Sprintf("Device silent: %s", dev.Hostname),
				Message: fmt.Sprintf("No logs received from %s (%s) in over %d minutes. Last seen: %s",
					dev.Hostname, dev.FromHostIP, silentAfterMin, dev.LastSeen.Format(time.RFC3339)),
				Severity: severity,
			})
		}
	}
}
