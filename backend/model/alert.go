package model

import (
	"encoding/json"
	"time"
)

// Alert rule types.
const (
	RuleTypeLogThreshold  = "log_threshold"
	RuleTypeDeviceSilence = "device_silence"
	RuleTypeConfigChange  = "config_change"
)

// Notification channel types.
const (
	ChannelTypeEmail   = "email"
	ChannelTypeWebhook = "webhook"
	ChannelTypeSlack   = "slack"
	ChannelTypeTeams   = "teams"
	ChannelTypeInApp   = "in_app"
)

// Field condition operators, evaluated against a log entry's parsed_fields.
const (
	FieldOpEquals    = "equals"
	FieldOpContains  = "contains"
	FieldOpNotEquals = "not_equals"
	FieldOpRegex     = "regex"
)

type AlertFieldCondition struct {
	ID        int64  `json:"id,omitempty"`
	FieldName string `json:"field_name"`
	Operator  string `json:"operator"`
	Value     string `json:"value"`
}

type Alert struct {
	ID                int64                 `json:"id"`
	Name              string                `json:"name"`
	Description       string                `json:"description"`
	RuleType          string                `json:"rule_type"`
	Severity          string                `json:"severity,omitempty"`
	DeviceIPs         []string              `json:"device_ips"`
	ParserNames       []string              `json:"parser_names"`
	FieldConditions   []AlertFieldCondition `json:"field_conditions"`
	MessagePattern    string                `json:"message_pattern,omitempty"`
	Threshold         int                   `json:"threshold"`
	WindowMinutes     int                   `json:"window_minutes"`
	CooldownMinutes   int                   `json:"cooldown_minutes"`
	AuditActionFilter string                `json:"audit_action_filter,omitempty"`
	IsActive          bool                  `json:"is_active"`
	CreatedBy         *int64                `json:"created_by,omitempty"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	LastFiredAt       *time.Time            `json:"last_fired_at,omitempty"`
	ChannelIDs        []int64               `json:"channel_ids"`
}

type AlertRequest struct {
	Name              string                `json:"name" binding:"required,max=255"`
	Description       string                `json:"description"`
	RuleType          string                `json:"rule_type" binding:"required,oneof=log_threshold device_silence config_change"`
	Severity          string                `json:"severity"`
	DeviceIPs         []string              `json:"device_ips"`
	ParserNames       []string              `json:"parser_names"`
	FieldConditions   []AlertFieldCondition `json:"field_conditions"`
	MessagePattern    string                `json:"message_pattern"`
	Threshold         int                   `json:"threshold"`
	WindowMinutes     int                   `json:"window_minutes"`
	CooldownMinutes   int                   `json:"cooldown_minutes"`
	AuditActionFilter string                `json:"audit_action_filter"`
	IsActive          *bool                 `json:"is_active"`
	ChannelIDs        []int64               `json:"channel_ids"`
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
	Type    string          `json:"type" binding:"required,oneof=email webhook slack teams in_app"`
	Config  json.RawMessage `json:"config"`
	Secret  string          `json:"secret"`
	Enabled *bool           `json:"enabled"`
}

type NotificationLogEntry struct {
	ID          int64     `json:"id"`
	AlertID     *int64    `json:"alert_id,omitempty"`
	AlertName   string    `json:"alert_name"`
	ChannelID   *int64    `json:"channel_id,omitempty"`
	ChannelName string    `json:"channel_name"`
	ChannelType string    `json:"channel_type"`
	Status      string    `json:"status"`
	Detail      string    `json:"detail,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type InAppNotification struct {
	ID        int64     `json:"id"`
	AlertID   *int64    `json:"alert_id,omitempty"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	Severity  string    `json:"severity"`
	CreatedAt time.Time `json:"created_at"`
}
