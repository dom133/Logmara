package alertengine

import (
	"database/sql"
	"fmt"
	"time"

	"syslytics/db"
	"syslytics/model"
	"syslytics/notify"
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

// CheckDeviceSilence fires any active device_silence rule whose matching
// device(s) haven't logged anything in over Threshold minutes. Intended to
// be called periodically (see the ticker in main.go), independently on
// every api replica - safe because the per-rule-per-device cooldown key
// dedupes duplicate fires the same way log_threshold rules do.
func (e *Engine) CheckDeviceSilence(database *sql.DB) {
	if db.GetSetting(database, "notifications_enabled", "true") != "true" {
		return
	}

	rules, err := db.GetActiveAlertsByType(database, model.RuleTypeDeviceSilence)
	if err != nil || len(rules) == 0 {
		return
	}

	devices, err := loadDeviceLastSeen(database)
	if err != nil || len(devices) == 0 {
		return
	}

	now := time.Now()
	for _, rule := range rules {
		silentAfterMin := rule.Threshold
		if silentAfterMin <= 0 {
			silentAfterMin = 15
		}
		cooldown := time.Duration(rule.CooldownMinutes) * time.Minute

		for _, dev := range devices {
			if !matchDevice(rule.DeviceIPs, dev.FromHostIP) {
				continue
			}
			if now.Sub(dev.LastSeen) < time.Duration(silentAfterMin)*time.Minute {
				continue
			}

			key := fmt.Sprintf("%d:%s", rule.ID, dev.FromHostIP)
			if !e.store.shouldFire(key, 1, 1, time.Minute, cooldown) {
				continue
			}

			_ = db.MarkAlertFired(database, rule.ID)
			e.dispatcher.DispatchAlert(rule, notify.Payload{
				Title: fmt.Sprintf("Device silent: %s", dev.Hostname),
				Message: fmt.Sprintf("No logs received from %s (%s) in over %d minutes. Last seen: %s",
					dev.Hostname, dev.FromHostIP, silentAfterMin, dev.LastSeen.Format(time.RFC3339)),
				Severity: "warning",
			})
		}
	}
}
