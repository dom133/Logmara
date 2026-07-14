package model

import (
	"encoding/json"
	"time"
)

type Parser struct {
	ID          int64         `json:"id"`
	Name        string        `json:"name"`
	Description *string       `json:"description,omitempty"`
	DeviceType  string        `json:"device_type"`
	MatchType   string        `json:"match_type"`
	MatchValue  *string       `json:"match_value"`
	Regex       string        `json:"regex"`
	Enabled     bool          `json:"enabled"`
	IsBuiltin   bool          `json:"is_builtin"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Fields      []ParsedField `json:"fields"`
}

type ParsedField struct {
	ID         int64  `json:"id"`
	ParserID   int64  `json:"parser_id"`
	ParserName string `json:"parser_name"`
	Name       string `json:"field_name"`
	Label      string `json:"field_label"`
	Type       string `json:"field_type"`
}

type Dashboard struct {
	ID             int64            `json:"id"`
	Name           string           `json:"name"`
	Description    *string          `json:"description,omitempty"`
	OwnerID        int64            `json:"owner_id"`
	OwnerUsername  string           `json:"owner_username"`
	Pinned         bool             `json:"pinned"`
	IsPublic       bool             `json:"is_public"`
	Config         json.RawMessage  `json:"config"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

type DashboardConfig struct {
	Devices []string          `json:"devices"`
	Fields  []string          `json:"fields"`
	Filters DashboardFilters  `json:"filters"`
}

type DashboardFilters struct {
	Severity string `json:"severity,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Search   string `json:"search,omitempty"`
}

type DashboardDataResponse struct {
	Logs    []SyslogLog `json:"logs"`
	Total   int64       `json:"total"`
	Fields  []string    `json:"fields"`
	Devices []string    `json:"devices"`
}

type ParserTestRequest struct {
	Pattern    string `json:"pattern"`
	SampleLog  string `json:"sample_log"`
}

type ParserTestResponse struct {
	Matched bool              `json:"matched"`
	Fields  map[string]string `json:"fields"`
}

type ReparseRequest struct {
	Hostname string `json:"hostname,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type ReparseResponse struct {
	Processed int `json:"processed"`
	Updated   int `json:"updated"`
}

func (c *DashboardConfig) IsValid() bool {
	return len(c.Fields) > 0
}

func ParseDashboardConfig(raw json.RawMessage) (*DashboardConfig, error) {
	var cfg DashboardConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}