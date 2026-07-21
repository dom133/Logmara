package notify

import "syslog-gui/model"

// Payload is a single notification event, independent of which channel
// type ends up delivering it.
type Payload struct {
	Title    string
	Message  string
	Severity string // info | warning | error | critical
	Link     string // optional deep link back into the app

	// TriggerLog and MatchedConditions are recorded in notification_log for
	// the alert history's "Details" view - they aren't sent to external
	// channels, only Title/Message/Severity are.
	TriggerLog        *model.TriggerLogSnapshot
	MatchedConditions []string
}

// Notifier delivers a Payload through one specific channel.
type Notifier interface {
	Send(payload Payload) error
}
