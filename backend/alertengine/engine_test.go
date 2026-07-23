package alertengine

import "testing"

func TestMeetsSeverity(t *testing.T) {
	tests := []struct {
		entry       string
		min         string
		expectPass  bool
	}{
		{"emerg", "error", true},
		{"info", "warning", false},
		{"error", "warning", true},
		{"debug", "info", false},
		{"crit", "critical", true},
		{"alert", "emerg", false},
		{"notice", "", true},
		{"unknown_sev", "error", true},
		{"error", "unknown_min", true},
	}
	for _, tc := range tests {
		got := meetsSeverity(tc.entry, tc.min)
		if got != tc.expectPass {
			t.Errorf("meetsSeverity(%q, %q) = %v; want %v", tc.entry, tc.min, got, tc.expectPass)
		}
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern  string
		value    string
		expected bool
	}{
		{"", "anything", true},
		{"hello", "HELLO WORLD", true},
		{"hello", "goodbye", false},
		{"*.log", "app.log", true},
		{"*.log", "app.txt", false},
		{"*test*", "my_test_file", true},
		{"exact", "exact", true},
	}
	for _, tc := range tests {
		got := matchPattern(tc.pattern, tc.value)
		if got != tc.expected {
			t.Errorf("matchPattern(%q, %q) = %v; want %v", tc.pattern, tc.value, got, tc.expected)
		}
	}
}

func TestNotifySeverity(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"emerg", "critical"},
		{"alert", "critical"},
		{"crit", "critical"},
		{"err", "error"},
		{"warning", "warning"},
		{"info", "info"},
		{"debug", "info"},
	}
	for _, tc := range tests {
		got := notifySeverity(tc.input)
		if got != tc.expect {
			t.Errorf("notifySeverity(%q) = %q; want %q", tc.input, got, tc.expect)
		}
	}
}