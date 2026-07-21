package notify

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"syslog-gui/db"
	"syslog-gui/model"
)

// BuildNotifier constructs the Sender for a channel given its decrypted
// secret (empty string if the channel has none). database is only needed to
// read the shared SMTP relay settings for email channels - individual email
// channels only carry a recipient list, not their own SMTP credentials.
func BuildNotifier(database *sql.DB, channel model.NotificationChannel, secret string) (Notifier, error) {
	switch channel.Type {
	case model.ChannelTypeEmail:
		if db.GetSetting(database, "smtp_enabled", "false") != "true" {
			return nil, fmt.Errorf("SMTP is disabled (enable it under Admin > Settings)")
		}
		var cfg struct {
			To []string `json:"to"`
		}
		if len(channel.Config) > 0 {
			if err := json.Unmarshal(channel.Config, &cfg); err != nil {
				return nil, fmt.Errorf("invalid email channel config: %w", err)
			}
		}
		return &EmailNotifier{
			SMTP: SMTPConfig{
				Host:     db.GetSetting(database, "smtp_host", ""),
				Port:     db.GetSetting(database, "smtp_port", "587"),
				Username: db.GetSetting(database, "smtp_username", ""),
				Password: db.GetSetting(database, "smtp_password", ""),
				From:     db.GetSetting(database, "smtp_from", ""),
				UseTLS:   db.GetSetting(database, "smtp_use_tls", "true") == "true",
			},
			To: cfg.To,
		}, nil

	case model.ChannelTypeWebhook:
		var cfg struct {
			URL string `json:"url"`
		}
		if len(channel.Config) > 0 {
			if err := json.Unmarshal(channel.Config, &cfg); err != nil {
				return nil, fmt.Errorf("invalid webhook channel config: %w", err)
			}
		}
		return &WebhookNotifier{URL: cfg.URL, BearerToken: secret, Client: defaultHTTPClient}, nil

	case model.ChannelTypeSlack:
		var cfg struct {
			WebhookURL string `json:"webhook_url"`
		}
		if len(channel.Config) > 0 {
			if err := json.Unmarshal(channel.Config, &cfg); err != nil {
				return nil, fmt.Errorf("invalid slack channel config: %w", err)
			}
		}
		return &SlackNotifier{WebhookURL: cfg.WebhookURL, Client: defaultHTTPClient}, nil

	case model.ChannelTypeTeams:
		var cfg struct {
			WebhookURL string `json:"webhook_url"`
		}
		if len(channel.Config) > 0 {
			if err := json.Unmarshal(channel.Config, &cfg); err != nil {
				return nil, fmt.Errorf("invalid teams channel config: %w", err)
			}
		}
		return &TeamsNotifier{WebhookURL: cfg.WebhookURL, Client: defaultHTTPClient}, nil

	default:
		return nil, fmt.Errorf("channel type %q has no external sender", channel.Type)
	}
}

// Dispatcher fans a firing alert out to all of its assigned channels and
// records the outcome of each attempt in notification_log.
type Dispatcher struct {
	DB *sql.DB
	// OnInApp, if set, is called after an in-app notification is persisted so
	// the caller can fan it out to connected clients (e.g. over SSE).
	OnInApp func(model.InAppNotification)
}

func NewDispatcher(database *sql.DB) *Dispatcher {
	return &Dispatcher{DB: database}
}

func (d *Dispatcher) DispatchAlert(alert model.Alert, payload Payload) {
	channels, err := db.GetChannelsForAlert(d.DB, alert.ID)
	if err != nil {
		return
	}
	alertID := alert.ID
	// One id per firing, shared by every channel's notification_log row
	// below, so the alert history can group them back into a single
	// "this rule fired, here's what happened per channel" entry instead of
	// showing one row per channel per firing.
	firingID := uuid.New().String()

	if len(channels) == 0 {
		// The rule fired but has nothing to deliver to - record that it fired
		// at all, otherwise there is no trace of it anywhere in the history.
		_ = db.LogNotification(d.DB, model.NotificationLogEntry{
			AlertID: &alertID, AlertName: alert.Name, FiringID: firingID,
			ChannelName: "(none)", Status: "no_channel", Detail: "Rule fired but has no notification channels attached",
			TriggerLog: payload.TriggerLog, MatchedConditions: payload.MatchedConditions,
		})
		return
	}

	for _, ch := range channels {
		d.dispatchOne(&alertID, alert.Name, firingID, ch, payload)
	}
}

func (d *Dispatcher) dispatchOne(alertID *int64, alertName, firingID string, ch model.NotificationChannel, payload Payload) {
	status, detail := "sent", ""
	var inAppID *int64

	if ch.Type == model.ChannelTypeInApp {
		id, createdAt, err := db.CreateInAppNotification(d.DB, alertID, payload.Title, payload.Message, payload.Severity)
		if err != nil {
			status, detail = "failed", err.Error()
		} else {
			inAppID = &id
			if d.OnInApp != nil {
				d.OnInApp(model.InAppNotification{ID: id, AlertID: alertID, Title: payload.Title, Message: payload.Message, Severity: payload.Severity, CreatedAt: createdAt})
			}
		}
	} else if ch.Type == model.ChannelTypePush {
		status, detail = sendPushChannel(d.DB, payload)
	} else {
		secret, err := db.DecryptChannelSecret(d.DB, ch.ID)
		if err != nil {
			status, detail = "failed", "decrypt channel secret: "+err.Error()
		} else if notifier, err := BuildNotifier(d.DB, ch, secret); err != nil {
			status, detail = "failed", err.Error()
		} else if err := notifier.Send(payload); err != nil {
			status, detail = "failed", err.Error()
		}
	}

	_ = db.LogNotification(d.DB, model.NotificationLogEntry{
		AlertID: alertID, AlertName: alertName, FiringID: firingID, ChannelID: &ch.ID, ChannelName: ch.Name, ChannelType: ch.Type,
		Status: status, Detail: detail,
		TriggerLog: payload.TriggerLog, MatchedConditions: payload.MatchedConditions,
		InAppNotificationID: inAppID,
	})
}

// sendPushChannel fans a payload out to every subscribed browser and
// collapses the per-device results into the single status/detail pair the
// notification_log records for this channel.
func sendPushChannel(database *sql.DB, payload Payload) (status, detail string) {
	res, err := dispatchPush(database, payload)
	if err != nil {
		return "failed", err.Error()
	}
	if res.Delivered == 0 {
		return "failed", fmt.Sprintf("all %d push deliveries failed: %s", res.Failed, strings.Join(res.Errors, "; "))
	}
	if res.Failed > 0 {
		return "sent", fmt.Sprintf("delivered to %d device(s), %d failed: %s", res.Delivered, res.Failed, strings.Join(res.Errors, "; "))
	}
	return "sent", fmt.Sprintf("delivered to %d device(s)", res.Delivered)
}

// TestChannel sends a fixed test payload directly to a single channel,
// bypassing alert association and notification_log - used by the "Test"
// button in the admin UI. onInApp, if set, is called for an in_app channel
// so the caller can fan the test notification out over SSE the same way a
// real alert firing would - otherwise it would only ever show up after the
// browser reloads and re-fetches the recent list.
func TestChannel(database *sql.DB, channel model.NotificationChannel, onInApp func(model.InAppNotification)) error {
	payload := Payload{
		Title:    "Test notification",
		Message:  "This is a test notification from SysLog GUI.",
		Severity: "info",
	}

	if channel.Type == model.ChannelTypeInApp {
		id, createdAt, err := db.CreateInAppNotification(database, nil, payload.Title, payload.Message, payload.Severity)
		if err != nil {
			return err
		}
		if onInApp != nil {
			onInApp(model.InAppNotification{ID: id, Title: payload.Title, Message: payload.Message, Severity: payload.Severity, CreatedAt: createdAt})
		}
		return nil
	}

	if channel.Type == model.ChannelTypePush {
		res, err := dispatchPush(database, payload)
		if err != nil {
			return err
		}
		if res.Delivered == 0 {
			return fmt.Errorf("all %d push deliveries failed: %s", res.Failed, strings.Join(res.Errors, "; "))
		}
		return nil
	}

	secret, err := db.DecryptChannelSecret(database, channel.ID)
	if err != nil {
		return fmt.Errorf("decrypt channel secret: %w", err)
	}
	notifier, err := BuildNotifier(database, channel, secret)
	if err != nil {
		return err
	}
	return notifier.Send(payload)
}
