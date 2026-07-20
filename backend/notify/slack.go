package notify

import (
	"fmt"
	"net/http"
)

type SlackNotifier struct {
	WebhookURL string
	Client     *http.Client
}

func (n *SlackNotifier) Send(payload Payload) error {
	text := fmt.Sprintf("*%s*\n%s", payload.Title, payload.Message)
	if payload.Link != "" {
		text += "\n" + payload.Link
	}
	w := &WebhookNotifier{URL: n.WebhookURL, Client: n.Client}
	return w.post(map[string]string{"text": text})
}
