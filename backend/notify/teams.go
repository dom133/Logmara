package notify

import "net/http"

type TeamsNotifier struct {
	WebhookURL string
	Client     *http.Client
}

func (n *TeamsNotifier) Send(payload Payload) error {
	card := map[string]any{
		"@type":    "MessageCard",
		"@context": "http://schema.org/extensions",
		"summary":  payload.Title,
		"title":    payload.Title,
		"text":     payload.Message,
	}
	if payload.Link != "" {
		card["potentialAction"] = []map[string]any{
			{
				"@type":   "OpenUri",
				"name":    "View in Logmara",
				"targets": []map[string]string{{"os": "default", "uri": payload.Link}},
			},
		}
	}
	w := &WebhookNotifier{URL: n.WebhookURL, Client: n.Client}
	return w.post(card)
}
