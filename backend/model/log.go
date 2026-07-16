package model

import (
	"database/sql"
	"encoding/json"
	"time"
)

type SeverityCounts map[string]int64

type SyslogLog struct {
	ID             int64             `json:"id"`
	Timestamp      time.Time         `json:"timestamp"`
	Hostname       string            `json:"hostname"`
	FromHostIP     *string           `json:"fromhost_ip,omitempty"`
	AppName        *string           `json:"app_name,omitempty"`
	ProcessID      *string           `json:"process_id,omitempty"`
	MsgID          *string           `json:"msg_id,omitempty"`
	Severity       string            `json:"severity"`
	Facility       *string           `json:"facility,omitempty"`
	Message        string            `json:"message"`
	RawMessage     *string           `json:"raw_message,omitempty"`
	ParsedFields   map[string]string `json:"parsed_fields,omitempty"`
	MatchedParsers []string          `json:"matched_parsers,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

type IngestEntry struct {
	Timestamp      string   `json:"timestamp"`
	Hostname       string   `json:"hostname"`
	FromHostIP     string   `json:"fromhost_ip"`
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
		Timestamp            string `json:"timestamp"`
		TimeReported         string `json:"timereported"`
		Hostname             string `json:"hostname"`
		FromHostIP           string `json:"fromhost-ip"`
		FromHostIPUnderscore string `json:"fromhost_ip"`
		AppName              string `json:"app_name"`
		ProgramName          string `json:"programname"`
		ProcessID            string `json:"process_id"`
		ProcID               string `json:"procid"`
		Pid                  string `json:"pid"`
		MsgID                string `json:"msg_id"`
		Severity             string `json:"severity"`
		SeverityText         string `json:"severity_text"`
		SyslogSevText        string `json:"syslogseverity-text"`
		Facility             string `json:"facility"`
		FacilityText         string `json:"facility_text"`
		SyslogFacText        string `json:"syslogfacility-text"`
		Message              string `json:"message"`
		Msg                  string `json:"msg"`
		RawMessage           string `json:"raw_message"`
		AtTimestamp          string `json:"@timestamp"`
		Program              string `json:"program"`
		SyslogTag            string `json:"syslog_tag"`
	}

	var raw RawEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	e.Timestamp = raw.Timestamp
	if e.Timestamp == "" {
		e.Timestamp = raw.AtTimestamp
	}
	if e.Timestamp == "" {
		e.Timestamp = raw.TimeReported
	}
	e.FromHostIP = raw.FromHostIP
	if e.FromHostIP == "" {
		e.FromHostIP = raw.FromHostIPUnderscore
	}
	e.Hostname = raw.Hostname
	if e.Hostname == "" {
		e.Hostname = raw.FromHostIP
	}
	e.Severity = raw.Severity
	if e.Severity == "" {
		e.Severity = raw.SeverityText
	}
	if e.Severity == "" {
		e.Severity = raw.SyslogSevText
	}
	e.Facility = raw.Facility
	if e.Facility == "" {
		e.Facility = raw.FacilityText
	}
	if e.Facility == "" {
		e.Facility = raw.SyslogFacText
	}
	e.AppName = raw.AppName
	if e.AppName == "" {
		e.AppName = raw.Program
	}
	if e.AppName == "" {
		e.AppName = raw.ProgramName
	}
	e.ProcessID = raw.ProcessID
	if e.ProcessID == "" {
		e.ProcessID = raw.Pid
	}
	if e.ProcessID == "" {
		e.ProcessID = raw.ProcID
	}
	e.Message = raw.Message
	if e.Message == "" {
		e.Message = raw.Msg
	}
	e.RawMessage = raw.RawMessage
	if e.RawMessage == "" {
		e.RawMessage = e.Message
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
	TotalLogs      int64            `json:"total_logs"`
	LogsLastHour   int64            `json:"logs_last_hour"`
	LogsLastDay    int64            `json:"logs_last_day"`
	UniqueDevices  int64            `json:"unique_devices"`
	SeverityCounts map[string]int64 `json:"severity_counts"`
	TopDevices     []DeviceCount    `json:"top_devices"`
	TopErrors      []ErrorMessage   `json:"top_errors"`
}

type DeviceCount struct {
	Hostname   string `json:"hostname"`
	FromHostIP string `json:"fromhost_ip"`
	Count      int64  `json:"count"`
}

type ErrorMessage struct {
	Message    string `json:"message"`
	Count      int64  `json:"count"`
	Hostname   string `json:"hostname"`
	FromHostIP string `json:"fromhost_ip"`
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
	FromHostIP     string         `json:"fromhost_ip"`
	Hostname       string         `json:"hostname"`
	OldHostname    string         `json:"old_hostname,omitempty"`
	DisplayName    string         `json:"display_name,omitempty"`
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
