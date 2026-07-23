package parser

import (
	"testing"
)

func TestParseRFC3164(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantHost    string
		wantProgram string
		wantSeverity string
	}{
		{
			name:        "standard syslog",
			line:        "<134>Oct 11 22:14:15 myhost myprogram[1234]: message text",
			wantHost:    "myhost",
			wantProgram: "myprogram",
			wantSeverity: "notice",
		},
		{
			name:        "emergency",
			line:        "<0>Jan  1 00:00:00 localhost kernel: panic occurred",
			wantHost:    "localhost",
			wantProgram: "kernel",
			wantSeverity: "emerg",
		},
		{
			name:        "debug level",
			line:        "<191>Dec 25 12:00:00 webserver nginx: debug info",
			wantHost:    "webserver",
			wantProgram: "nginx",
			wantSeverity: "debug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRFC3164(tt.line)
			if result.Hostname != tt.wantHost {
				t.Errorf("hostname = %q, want %q", result.Hostname, tt.wantHost)
			}
			if result.AppName != tt.wantProgram {
				t.Errorf("appname = %q, want %q", result.AppName, tt.wantProgram)
			}
			if result.Severity != tt.wantSeverity {
				t.Errorf("severity = %q, want %q", result.Severity, tt.wantSeverity)
			}
		})
	}
}

func TestParseRFC5424(t *testing.T) {
	line := "<34>1 2023-10-11T22:14:15.123Z myhost myprogram 1234 - - message here"
	result := ParseRFC5424(line)
	if result.Hostname != "myhost" {
		t.Errorf("hostname = %q, want %q", result.Hostname, "myhost")
	}
	if result.AppName != "myprogram" {
		t.Errorf("appname = %q, want %q", result.AppName, "myprogram")
	}
}

func TestParseUnknown(t *testing.T) {
	result := Parse("not a syslog line")
	if result.RawMessage != "not a syslog line" {
		t.Errorf("raw = %q, want %q", result.RawMessage, "not a syslog line")
	}
}