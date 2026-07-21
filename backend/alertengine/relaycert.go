package alertengine

import (
	"database/sql"
	"fmt"
	"time"

	"syslytics/db"
	"syslytics/model"
	"syslytics/notify"
)

const defaultRelayCertWarningDays = 30

// CheckRelayCertExpiring fires any active relay_cert_expiring rule for
// every "issued" relay certificate that's within the rule's warning window
// of its own expiry. Rule.Threshold doubles as "warn this many days before
// expiry" here (default 30, same reasoning as device_silence reusing
// Threshold for "silent for this many minutes" - it's already the numeric
// field every rule type has, no schema change needed for a second one).
// Intended to be called periodically (see the ticker in main.go).
func (e *Engine) CheckRelayCertExpiring(database *sql.DB) {
	if db.GetSetting(database, "notifications_enabled", "true") != "true" {
		return
	}

	rules, err := db.GetActiveAlertsByType(database, model.RuleTypeRelayCertExpiring)
	if err != nil || len(rules) == 0 {
		return
	}

	for _, rule := range rules {
		warnDays := rule.Threshold
		if warnDays <= 0 {
			warnDays = defaultRelayCertWarningDays
		}
		cooldown := time.Duration(rule.CooldownMinutes) * time.Minute

		certs, err := db.GetExpiringRelayCertificates(database, time.Duration(warnDays)*24*time.Hour)
		if err != nil || len(certs) == 0 {
			continue
		}

		for _, cert := range certs {
			key := fmt.Sprintf("%d:%d", rule.ID, cert.ID)
			if !e.store.shouldFire(key, 1, 1, time.Minute, cooldown) {
				continue
			}

			_ = db.MarkAlertFired(database, rule.ID)
			daysLeft := int(time.Until(cert.ExpiresAt).Hours() / 24)
			e.dispatcher.DispatchAlert(rule, notify.Payload{
				Title: fmt.Sprintf("Relay certificate expiring: %s", cert.Label),
				Message: fmt.Sprintf("Relay certificate %q expires %s (in %d day(s)). Renew it from Admin > Syslog Relay > Certificates before it lapses.",
					cert.Label, cert.ExpiresAt.Format(time.RFC3339), daysLeft),
				Severity: "warning",
			})
		}
	}
}
