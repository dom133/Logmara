package model

import (
	"encoding/json"
	"time"
)

// Alert rule types.
const (
	RuleTypeLogThreshold      = "log_threshold"
	RuleTypeDeviceSilence     = "device_silence"
	RuleTypeConfigChange      = "config_change"
	RuleTypeRelayCertExpiring = "relay_cert_expiring"
)

// Notification channel types.
const (
	ChannelTypeEmail   = "email"
	ChannelTypeWebhook = "webhook"
	ChannelTypeSlack   = "slack"
	ChannelTypeTeams   = "teams"
	ChannelTypeInApp   = "in_app"
	ChannelTypePush    = "push"
)

// Field condition operators, evaluated against a log entry's parsed_fields.
const (
	FieldOpEquals    = "equals"
	FieldOpContains  = "contains"
	FieldOpNotEquals = "not_equals"
	FieldOpRegex     = "regex"
)

// Field condition logic: how a rule's field conditions combine, when it has
// more than one.
const (
	FieldConditionsLogicAnd = "and"
	FieldConditionsLogicOr  = "or"
)

type AlertFieldCondition struct {
	ID        int64  `json:"id,omitempty"`
	FieldName string `json:"field_name"`
	Operator  string `json:"operator"`
	Value     string `json:"value"`
}

type Alert struct {
	ID              int64                 `json:"id"`
	Name            string                `json:"name"`
	Description     string                `json:"description"`
	RuleType        string                `json:"rule_type"`
	Severity        string                `json:"severity,omitempty"`
	DeviceIPs       []string              `json:"device_ips"`
	ParserNames     []string              `json:"parser_names"`
	FieldConditions []AlertFieldCondition `json:"field_conditions"`
	// FieldConditionsLogic is how multiple FieldConditions combine:
	// FieldConditionsLogicAnd (default, every condition must match) or
	// FieldConditionsLogicOr (any one condition matching is enough).
	FieldConditionsLogic string `json:"field_conditions_logic"`
	MessagePattern       string `json:"message_pattern,omitempty"`
	Threshold            int    `json:"threshold"`
	WindowMinutes        int    `json:"window_minutes"`
	CooldownMinutes      int    `json:"cooldown_minutes"`
	// FireOnEveryMatch, when set, makes a log_threshold rule notify once per
	// matching log entry instead of accumulating matches against Threshold/
	// WindowMinutes and gating repeats behind CooldownMinutes.
	FireOnEveryMatch  bool       `json:"fire_on_every_match"`
	AuditActionFilter string     `json:"audit_action_filter,omitempty"`
	IsActive          bool       `json:"is_active"`
	CreatedBy         *int64     `json:"created_by,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LastFiredAt       *time.Time `json:"last_fired_at,omitempty"`
	ChannelIDs        []int64    `json:"channel_ids"`
}

type AlertRequest struct {
	Name                 string                `json:"name" binding:"required,max=255"`
	Description          string                `json:"description"`
	RuleType             string                `json:"rule_type" binding:"required,oneof=log_threshold device_silence config_change relay_cert_expiring"`
	Severity             string                `json:"severity"`
	DeviceIPs            []string              `json:"device_ips"`
	ParserNames          []string              `json:"parser_names"`
	FieldConditions      []AlertFieldCondition `json:"field_conditions"`
	FieldConditionsLogic string                `json:"field_conditions_logic"`
	MessagePattern       string                `json:"message_pattern"`
	Threshold            int                   `json:"threshold"`
	WindowMinutes        int                   `json:"window_minutes"`
	CooldownMinutes      int                   `json:"cooldown_minutes"`
	FireOnEveryMatch     bool                  `json:"fire_on_every_match"`
	AuditActionFilter    string                `json:"audit_action_filter"`
	IsActive             *bool                 `json:"is_active"`
	ChannelIDs           []int64               `json:"channel_ids"`
}

type NotificationChannel struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Config    json.RawMessage `json:"config"`
	HasSecret bool            `json:"has_secret"`
	Enabled   bool            `json:"enabled"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type NotificationChannelRequest struct {
	Name    string          `json:"name" binding:"required,max=255"`
	Type    string          `json:"type" binding:"required,oneof=email webhook slack teams in_app push"`
	Config  json.RawMessage `json:"config"`
	Secret  string          `json:"secret"`
	Enabled *bool           `json:"enabled"`
}

// TriggerLogSnapshot is a self-contained copy of the log entry that caused
// an alert to fire. It's stored alongside the notification log entry
// (rather than just a reference to syslog_logs) so the alert history's
// "Details" view still shows the triggering log after retention cleanup has
// purged the original row.
type TriggerLogSnapshot struct {
	Timestamp  string `json:"timestamp"`
	Hostname   string `json:"hostname"`
	FromHostIP string `json:"fromhost_ip"`
	AppName    string `json:"app_name,omitempty"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
}

type NotificationLogEntry struct {
	ID        int64  `json:"id"`
	AlertID   *int64 `json:"alert_id,omitempty"`
	AlertName string `json:"alert_name"`
	// FiringID is shared by every channel dispatched for the same rule
	// firing, so the alert history can group these rows back into a single
	// "this rule fired, here's what happened per channel" entry instead of
	// one row per channel per firing. Empty for rows written before this
	// column existed - the frontend falls back to treating each of those as
	// its own single-channel group.
	FiringID    string              `json:"firing_id,omitempty"`
	ChannelID   *int64              `json:"channel_id,omitempty"`
	ChannelName string              `json:"channel_name"`
	ChannelType string              `json:"channel_type"`
	Status      string              `json:"status"`
	Detail      string              `json:"detail,omitempty"`
	TriggerLog  *TriggerLogSnapshot `json:"trigger_log,omitempty"`
	// InAppNotificationID links this row back to the in_app_notifications
	// row shown in the bell dropdown (set only when ChannelType is
	// "in_app"), so clicking a bell notification can jump straight to and
	// open this same entry in the alert History tab.
	InAppNotificationID *int64    `json:"in_app_notification_id,omitempty"`
	MatchedConditions   []string  `json:"matched_conditions,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

type InAppNotification struct {
	ID        int64     `json:"id"`
	AlertID   *int64    `json:"alert_id,omitempty"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	Severity  string    `json:"severity"`
	CreatedAt time.Time `json:"created_at"`
}
