package alertengine

import (
	"testing"
	"time"
)

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern, value string
		want           bool
	}{
		{"", "anything", true},
		{"router", "core-router-1", true},
		{"router", "switch-1", false},
		{"router*", "router-1", true},
		{"router*", "core-router-1", false},
		{"*router*", "core-router-1", true},
		{"ROUTER", "core-router-1", true}, // substring match is case insensitive
		{"router-?", "router-1", false},   // '?' is not special, treated literally
	}
	for _, c := range cases {
		if got := matchPattern(c.pattern, c.value); got != c.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", c.pattern, c.value, got, c.want)
		}
	}
}

func TestMeetsSeverity(t *testing.T) {
	cases := []struct {
		entry, min string
		want       bool
	}{
		{"err", "", true},
		{"err", "warning", true},   // err is more severe than warning
		{"info", "warning", false}, // info is less severe than warning
		{"warning", "warning", true},
		{"unknown", "warning", true}, // unrecognized values always match
	}
	for _, c := range cases {
		if got := meetsSeverity(c.entry, c.min); got != c.want {
			t.Errorf("meetsSeverity(%q, %q) = %v, want %v", c.entry, c.min, got, c.want)
		}
	}
}

func TestNotifySeverity(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"emerg", "critical"},
		{"crit", "critical"},
		{"err", "error"},
		{"warning", "warning"},
		{"info", "info"},
		{"debug", "info"},
	}
	for _, c := range cases {
		if got := notifySeverity(c.in); got != c.want {
			t.Errorf("notifySeverity(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLocalCounterStore_ThresholdAndWindow(t *testing.T) {
	s := newLocalCounterStore()

	if s.shouldFire("rule1", 2, 5, time.Minute, time.Minute) {
		t.Fatal("should not fire below threshold")
	}
	if !s.shouldFire("rule1", 3, 5, time.Minute, time.Minute) {
		t.Fatal("should fire once threshold is reached (2+3=5)")
	}
}

func TestLocalCounterStore_Cooldown(t *testing.T) {
	s := newLocalCounterStore()

	if !s.shouldFire("rule1", 5, 5, time.Minute, time.Hour) {
		t.Fatal("expected first call to fire")
	}
	if s.shouldFire("rule1", 5, 5, time.Minute, time.Hour) {
		t.Fatal("expected second call within cooldown to not fire")
	}
}

func TestLocalCounterStore_WindowExpiry(t *testing.T) {
	s := newLocalCounterStore()

	if s.shouldFire("rule1", 2, 5, time.Nanosecond, time.Minute) {
		t.Fatal("should not fire below threshold")
	}
	time.Sleep(time.Millisecond)
	// the window from the first call has expired, so this call starts a
	// fresh count rather than accumulating on top of the expired one
	if s.shouldFire("rule1", 2, 5, time.Minute, time.Minute) {
		t.Fatal("expired window should not carry over its count")
	}
}

func TestLocalCounterStore_IndependentKeys(t *testing.T) {
	s := newLocalCounterStore()

	if !s.shouldFire("rule1:host-a", 1, 1, time.Minute, time.Hour) {
		t.Fatal("expected rule1:host-a to fire")
	}
	if !s.shouldFire("rule1:host-b", 1, 1, time.Minute, time.Hour) {
		t.Fatal("expected rule1:host-b to fire independently of host-a's cooldown")
	}
}
