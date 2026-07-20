package notify

import (
	"database/sql"
	"encoding/json"
	"fmt"

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

	if len(channels) == 0 {
		// The rule fired but has nothing to deliver to - record that it fired
		// at all, otherwise there is no trace of it anywhere in the history.
		_ = db.LogNotification(d.DB, model.NotificationLogEntry{
			AlertID: &alertID, AlertName: alert.Name,
			ChannelName: "(none)", Status: "no_channel", Detail: "Rule fired but has no notification channels attached",
		})
		return
	}

	for _, ch := range channels {
		d.dispatchOne(&alertID, alert.Name, ch, payload)
	}
}

func (d *Dispatcher) dispatchOne(alertID *int64, alertName string, ch model.NotificationChannel, payload Payload) {
	status, detail := "sent", ""

	if ch.Type == model.ChannelTypeInApp {
		id, err := db.CreateInAppNotification(d.DB, alertID, payload.Title, payload.Message, payload.Severity)
		if err != nil {
			status, detail = "failed", err.Error()
		} else if d.OnInApp != nil {
			d.OnInApp(model.InAppNotification{ID: id, AlertID: alertID, Title: payload.Title, Message: payload.Message, Severity: payload.Severity})
		}
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
		AlertID: alertID, AlertName: alertName, ChannelID: &ch.ID, ChannelName: ch.Name, ChannelType: ch.Type,
		Status: status, Detail: detail,
	})
}

// TestChannel sends a fixed test payload directly to a single channel,
// bypassing alert association and notification_log - used by the "Test"
// button in the admin UI.
func TestChannel(database *sql.DB, channel model.NotificationChannel) error {
	payload := Payload{
		Title:    "Test notification",
		Message:  "This is a test notification from SysLog GUI.",
		Severity: "info",
	}

	if channel.Type == model.ChannelTypeInApp {
		_, err := db.CreateInAppNotification(database, nil, payload.Title, payload.Message, payload.Severity)
		return err
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
