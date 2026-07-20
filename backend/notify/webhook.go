package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

var defaultHTTPClient = &http.Client{Timeout: 10 * time.Second}

type WebhookNotifier struct {
	URL         string
	BearerToken string // optional, decrypted from the channel's stored secret
	Client      *http.Client
}

type webhookBody struct {
	Title    string `json:"title"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Link     string `json:"link,omitempty"`
}

func (n *WebhookNotifier) Send(payload Payload) error {
	return n.post(webhookBody{Title: payload.Title, Message: payload.Message, Severity: payload.Severity, Link: payload.Link})
}

func (n *WebhookNotifier) post(body any) error {
	if n.URL == "" {
		return fmt.Errorf("webhook channel has no URL configured")
	}
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	client := n.Client
	if client == nil {
		client = defaultHTTPClient
	}

	req, err := http.NewRequest(http.MethodPost, n.URL, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if n.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+n.BearerToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}
