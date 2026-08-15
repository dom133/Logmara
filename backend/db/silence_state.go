package db

import (
	"database/sql"
	"fmt"
	"time"
)

// SilenceState is one (device_silence rule, device) pair currently
// considered silent - see alertengine.CheckDeviceSilence.
type SilenceState struct {
	RuleID       int64
	DeviceIP     string
	SilentSince  time.Time
	LastSeverity string
}

// ListDeviceSilenceStates returns every currently-tracked silent
// rule/device pair, keyed by "<rule_id>:<device_ip>", for CheckDeviceSilence
// to diff against the devices it just read from mv_device_stats.
func ListDeviceSilenceStates(database *sql.DB) (map[string]SilenceState, error) {
	rows, err := database.Query("SELECT rule_id, device_ip, silent_since, last_severity FROM device_silence_state")
	if err != nil {
		return nil, fmt.Errorf("list device silence states: %w", err)
	}
	defer rows.Close()

	states := make(map[string]SilenceState)
	for rows.Next() {
		var s SilenceState
		if err := rows.Scan(&s.RuleID, &s.DeviceIP, &s.SilentSince, &s.LastSeverity); err != nil {
			return nil, fmt.Errorf("scan device silence state: %w", err)
		}
		states[fmt.Sprintf("%d:%s", s.RuleID, s.DeviceIP)] = s
	}
	return states, nil
}

// UpsertDeviceSilenceState records/updates that ruleID+deviceIP is silent as
// of since, at the given severity.
func UpsertDeviceSilenceState(database *sql.DB, ruleID int64, deviceIP string, since time.Time, severity string) error {
	_, err := database.Exec(
		`INSERT INTO device_silence_state (rule_id, device_ip, silent_since, last_severity)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (rule_id, device_ip) DO UPDATE SET last_severity = EXCLUDED.last_severity`,
		ruleID, deviceIP, since, severity,
	)
	return err
}

// DeleteDeviceSilenceState clears ruleID+deviceIP's silent tracking, once the
// device has resumed logging (or the rule/device pairing no longer applies).
func DeleteDeviceSilenceState(database *sql.DB, ruleID int64, deviceIP string) error {
	_, err := database.Exec("DELETE FROM device_silence_state WHERE rule_id = $1 AND device_ip = $2", ruleID, deviceIP)
	return err
}
