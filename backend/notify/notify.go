package notify

// Payload is a single notification event, independent of which channel
// type ends up delivering it.
type Payload struct {
	Title    string
	Message  string
	Severity string // info | warning | error | critical
	Link     string // optional deep link back into the app
}

// Notifier delivers a Payload through one specific channel.
type Notifier interface {
	Send(payload Payload) error
}
