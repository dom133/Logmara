package notify

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"

	"syslog-gui/db"
)

// vapidSubject identifies this server to push services per RFC 8292. It
// doesn't need to be a deliverable address - push services only check that
// it's present, not that it resolves.
const vapidSubject = "admin@localhost"

// maxPushErrorBody caps how much of a push service's error response we keep,
// so a verbose HTML error page doesn't blow up notification_log.detail.
const maxPushErrorBody = 300

// sendWebPush delivers payload to a single browser push subscription.
// gone reports whether the endpoint is no longer valid (404/410), meaning
// the caller should delete the subscription rather than retry it later.
func sendWebPush(endpoint, p256dh, auth, publicKey, privateKey string, payload Payload) (gone bool, err error) {
	body, err := json.Marshal(map[string]string{
		"title": payload.Title,
		"body":  payload.Message,
		"url":   payload.Link,
	})
	if err != nil {
		return false, err
	}

	resp, err := webpush.SendNotification(body, &webpush.Subscription{
		Endpoint: endpoint,
		Keys:     webpush.Keys{P256dh: p256dh, Auth: auth},
	}, &webpush.Options{
		Subscriber:      vapidSubject,
		VAPIDPublicKey:  publicKey,
		VAPIDPrivateKey: privateKey,
		TTL:             60,
	})
	if err != nil {
		return false, fmt.Errorf("request to push service failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return true, fmt.Errorf("push subscription expired (status %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxPushErrorBody))
		detail := strings.TrimSpace(string(respBody))
		if detail == "" {
			return false, fmt.Errorf("push service returned status %d", resp.StatusCode)
		}
		return false, fmt.Errorf("push service returned status %d: %s", resp.StatusCode, detail)
	}
	return false, nil
}

// endpointHost returns just the host part of a push endpoint URL (e.g.
// fcm.googleapis.com) for logging, without leaking the per-subscription
// token that makes up the rest of the URL.
func endpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "(unparseable endpoint)"
	}
	return u.Host
}

// pushResult summarizes a fan-out of one payload to every subscribed
// browser. Errors holds a capped, deduplicated set of failure reasons so
// callers can surface something actionable instead of a bare count.
type pushResult struct {
	Delivered int
	Failed    int
	Errors    []string
}

const maxPushErrorsKept = 3

// dispatchPush fans payload out to every registered browser push
// subscription, pruning any the push service reports as expired. The
// returned error is only set for failures that happened before any
// subscription was attempted (no subscriptions registered, VAPID key
// generation failed, etc.) - per-subscription failures are reported via the
// returned pushResult instead.
func dispatchPush(database *sql.DB, payload Payload) (pushResult, error) {
	subs, err := db.GetAllPushSubscriptions(database)
	if err != nil {
		return pushResult{}, fmt.Errorf("load push subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return pushResult{}, fmt.Errorf("no browsers are subscribed to push notifications")
	}

	publicKey, privateKey, err := db.GetOrCreateVAPIDKeys(database)
	if err != nil {
		return pushResult{}, err
	}

	var res pushResult
	seen := make(map[string]bool)
	for _, s := range subs {
		gone, sendErr := sendWebPush(s.Endpoint, s.P256dh, s.Auth, publicKey, privateKey, payload)
		if gone {
			_ = db.DeletePushSubscription(database, s.Endpoint)
		}
		if sendErr != nil {
			res.Failed++
			slog.Warn("push delivery failed", "endpoint_host", endpointHost(s.Endpoint), "error", sendErr)
			if msg := sendErr.Error(); !seen[msg] && len(res.Errors) < maxPushErrorsKept {
				seen[msg] = true
				res.Errors = append(res.Errors, msg)
			}
		} else {
			res.Delivered++
		}
	}
	return res, nil
}
