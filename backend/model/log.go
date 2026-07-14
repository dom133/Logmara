package model

import (
	"database/sql"
	"encoding/json"
	"time"
)

type SeverityCounts map[string]int64

type SyslogLog struct {
	ID              int64             `json:"id"`
	Timestamp       time.Time         `json:"timestamp"`
	Hostname        string            `json:"hostname"`
	AppName         *string           `json:"app_name,omitempty"`
	ProcessID       *string           `json:"process_id,omitempty"`
	MsgID           *string           `json:"msg_id,omitempty"`
	Severity        string            `json:"severity"`
	Facility        *string           `json:"facility,omitempty"`
	Message         string            `json:"message"`
	RawMessage      *string           `json:"raw_message,omitempty"`
	ParsedFields    map[string]string `json:"parsed_fields,omitempty"`
	MatchedParsers  []string          `json:"matched_parsers,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
}

type IngestEntry struct {
	Timestamp      string   `json:"timestamp"`
	Hostname       string   `json:"hostname"`
	AppName        string   `json:"app_name"`
	ProcessID      string   `json:"process_id"`
	MsgID          string   `json:"msg_id"`
	Severity       string   `json:"severity"`
	Facility       string   `json:"facility"`
	Message        string   `json:"message"`
	RawMessage     string   `json:"raw_message"`
	ParsedFields   []byte   `json:"-"`
	MatchedParsers []string `json:"-"`
}

// UnmarshalJSON handles both old format and RSYSLOG_EF-JSON
func (e *IngestEntry) UnmarshalJSON(data []byte) error {
	type RawEntry struct {
		Timestamp    string `json:"timestamp"`
		Hostname     string `json:"hostname"`
		AppName      string `json:"app_name"`
		ProcessID    string `json:"process_id"`
		MsgID        string `json:"msg_id"`
		Severity     string `json:"severity"`
		Facility     string `json:"facility"`
		Message      string `json:"message"`
		RawMessage   string `json:"raw_message"`
		AtTimestamp  string `json:"@timestamp"`
		SeverityText string `json:"severity_text"`
		FacilityText string `json:"facility_text"`
		Program      string `json:"program"`
		Pid          string `json:"pid"`
		SyslogTag    string `json:"syslog_tag"`
	}

	var raw RawEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	e.Timestamp = raw.Timestamp
	if e.Timestamp == "" {
		e.Timestamp = raw.AtTimestamp
	}
	e.Hostname = raw.Hostname
	e.Severity = raw.Severity
	if e.Severity == "" {
		e.Severity = raw.SeverityText
	}
	e.Facility = raw.Facility
	if e.Facility == "" {
		e.Facility = raw.FacilityText
	}
	e.AppName = raw.AppName
	if e.AppName == "" {
		e.AppName = raw.Program
	}
	e.ProcessID = raw.ProcessID
	if e.ProcessID == "" {
		e.ProcessID = raw.Pid
	}
	e.Message = raw.Message
	e.RawMessage = raw.RawMessage
	if e.RawMessage == "" {
		e.RawMessage = raw.Message
	}
	if e.MsgID == "" && raw.SyslogTag != "" {
		e.MsgID = raw.SyslogTag
	}

	return nil
}

type LogQueryParams struct {
	Offset   int    `form:"offset"`
	Limit    int    `form:"limit"`
	Hostname string `form:"hostname"`
	Severity string `form:"severity"`
	AppName  string `form:"app_name"`
	Search   string `form:"search"`
	From     string `form:"from"`
	To       string `form:"to"`
	Sort     string `form:"sort"`
}

type DashboardStats struct {
	TotalLogs      int64             `json:"total_logs"`
	LogsLastHour   int64             `json:"logs_last_hour"`
	LogsLastDay    int64             `json:"logs_last_day"`
	UniqueDevices  int64             `json:"unique_devices"`
	SeverityCounts map[string]int64  `json:"severity_counts"`
	TopDevices     []DeviceCount     `json:"top_devices"`
	TopErrors      []ErrorMessage    `json:"top_errors"`
}

type DeviceCount struct {
	Hostname string `json:"hostname"`
	Count    int64  `json:"count"`
}

type ErrorMessage struct {
	Message  string `json:"message"`
	Count    int64  `json:"count"`
	Hostname string `json:"hostname"`
}

type TimelinePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Count     int64     `json:"count"`
}

type SeverityStats struct {
	Severity string `json:"severity"`
	Count    int64  `json:"count"`
}

type DeviceStats struct {
	Hostname       string         `json:"hostname"`
	TotalLogs      int64          `json:"total_logs"`
	LastSeen       sql.NullTime   `json:"-"`
	SeverityCount  SeverityCounts `json:"severity_count"`
	MatchedParsers []string       `json:"matched_parsers"`
	HasParsed      bool           `json:"has_parsed"`
}

func (d DeviceStats) MarshalJSON() ([]byte, error) {
	type Alias DeviceStats
	lastSeen := ""
	if d.LastSeen.Valid {
		lastSeen = d.LastSeen.Time.Format(time.RFC3339)
	}
	return json.Marshal(&struct {
		LastSeen string `json:"last_seen"`
		Alias
	}{
		LastSeen: lastSeen,
		Alias:    (Alias)(d),
	})
}

func ParseIngestEntry(raw []byte) (*IngestEntry, error) {
	var entry IngestEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}
