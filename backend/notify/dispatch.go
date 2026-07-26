package notify

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"syslytics/db"
	"syslytics/model"
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

	// Deep link back into this firing's Details view in the alert history -
	// used by push notifications (see notify/push.go) and any other channel
	// that renders Payload.Link. Set here (not by callers in alertengine)
	// because firingID doesn't exist until now. Only filled in when a caller
	// hasn't already set a more specific link.
	if payload.Link == "" {
		payload.Link = fmt.Sprintf("/alerts?tab=history&firing=%s", firingID)
	}

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

	// Deduplicate push and in_app: merge target user IDs across all channels
	// of the same type so each user receives at most one notification per firing.
	type dedupInfo struct {
		targets   map[int64]bool
		broadcast bool
	}
	dedup := map[string]*dedupInfo{
		model.ChannelTypePush:  {targets: map[int64]bool{}},
		model.ChannelTypeInApp: {targets: map[int64]bool{}},
	}
	firstByType := map[string]model.NotificationChannel{}
	for _, ch := range channels {
		info := dedup[ch.Type]
		if info != nil {
			var cfg struct{ UserIds []int64 `json:"user_ids"` }
			_ = json.Unmarshal(ch.Config, &cfg)
			if len(cfg.UserIds) == 0 {
				info.broadcast = true
			} else {
				for _, uid := range cfg.UserIds {
					info.targets[uid] = true
				}
			}
		}
		if _, exists := firstByType[ch.Type]; !exists {
			firstByType[ch.Type] = ch
		}
	}

	for _, ch := range channels {
		info := dedup[ch.Type]
		if info != nil && (info.broadcast || len(info.targets) > 0) {
			if ch.ID != firstByType[ch.Type].ID {
				continue
			}
			if info.broadcast {
				d.dispatchOne(&alertID, alert.Name, firingID, ch, payload, nil)
			} else {
				merged := make([]int64, 0, len(info.targets))
				for uid := range info.targets {
					merged = append(merged, uid)
				}
				d.dispatchOne(&alertID, alert.Name, firingID, ch, payload, merged)
			}
		} else {
			d.dispatchOne(&alertID, alert.Name, firingID, ch, payload, nil)
		}
	}
}

func (d *Dispatcher) dispatchOne(alertID *int64, alertName, firingID string, ch model.NotificationChannel, payload Payload, overrideTargetUserIds []int64) {
	var targetUserIds []int64
	if overrideTargetUserIds != nil {
		targetUserIds = overrideTargetUserIds
	} else if len(ch.Config) > 0 {
		var cfg struct {
			UserIds []int64 `json:"user_ids"`
		}
		_ = json.Unmarshal(ch.Config, &cfg)
		targetUserIds = cfg.UserIds
	}

	status, detail := "sent", ""
	var inAppID *int64

	if ch.Type == model.ChannelTypeInApp {
		id, createdAt, err := db.CreateInAppNotification(d.DB, alertID, payload.Title, payload.Message, payload.Severity, payload.AlertRuleType, targetUserIds)
		if err != nil {
			status, detail = "failed", "internal app notification failed"
		} else {
			inAppID = &id
			if d.OnInApp != nil {
				d.OnInApp(model.InAppNotification{ID: id, AlertID: *alertID, Title: payload.Title, Message: payload.Message, Severity: payload.Severity, AlertRuleType: payload.AlertRuleType, TargetUserIds: targetUserIds, CreatedAt: createdAt})
			}
		}
	} else if ch.Type == model.ChannelTypePush {
		status, detail = sendPushChannel(d.DB, payload, targetUserIds)
	} else {
		secret, err := db.DecryptChannelSecret(d.DB, ch.ID)
		if err != nil {
			status, detail = "failed", "decrypt channel secret failed"
		} else if notifier, err := BuildNotifier(d.DB, ch, secret); err != nil {
			status, detail = "failed", "build notifier failed"
		} else if err := notifier.Send(payload); err != nil {
			status, detail = "failed", "send notification failed"
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
func sendPushChannel(database *sql.DB, payload Payload, targetUserIds []int64) (status, detail string) {
	res, err := dispatchPush(database, payload, targetUserIds)
	if err != nil {
		return "failed", err.Error()
	}
	if res.Delivered == 0 {
		return "failed", fmt.Sprintf("all %d push deliveries failed: %s", res.Failed, strings.Join(res.Errors, "; "))
	}
	if res.Failed > 0 {
		return "partial", fmt.Sprintf("delivered to %d device(s), %d failed (not delivered to those): %s", res.Delivered, res.Failed, strings.Join(res.Errors, "; "))
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
		Message:  "This is a test notification from Syslytics.",
		Severity: "info",
	}

	var targetUserIds []int64
	if len(channel.Config) > 0 {
		var cfg struct {
			UserIds []int64 `json:"user_ids"`
		}
		_ = json.Unmarshal(channel.Config, &cfg)
		targetUserIds = cfg.UserIds
	}

	if channel.Type == model.ChannelTypeInApp {
		id, createdAt, err := db.CreateInAppNotification(database, nil, payload.Title, payload.Message, payload.Severity, "", targetUserIds)
		if err != nil {
			return err
		}
		if onInApp != nil {
			onInApp(model.InAppNotification{ID: id, Title: payload.Title, Message: payload.Message, Severity: payload.Severity, TargetUserIds: targetUserIds, CreatedAt: createdAt})
		}
		return nil
	}

	if channel.Type == model.ChannelTypePush {
		res, err := dispatchPush(database, payload, targetUserIds)
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
