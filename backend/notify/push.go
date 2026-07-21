package notify

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"

	"syslog-gui/db"
)

// vapidSubject identifies this server to push services per RFC 8292. It
// doesn't need to be a deliverable address - push services only check that
// it's present, not that it resolves.
const vapidSubject = "admin@localhost"

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
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return true, fmt.Errorf("push subscription expired (status %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf("push service returned status %d", resp.StatusCode)
	}
	return false, nil
}

// dispatchPush fans payload out to every registered browser push
// subscription, pruning any the push service reports as expired.
func dispatchPush(database *sql.DB, payload Payload) (delivered, failed int, err error) {
	subs, err := db.GetAllPushSubscriptions(database)
	if err != nil {
		return 0, 0, fmt.Errorf("load push subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return 0, 0, fmt.Errorf("no browsers are subscribed to push notifications")
	}

	publicKey, privateKey, err := db.GetOrCreateVAPIDKeys(database)
	if err != nil {
		return 0, 0, err
	}

	for _, s := range subs {
		gone, sendErr := sendWebPush(s.Endpoint, s.P256dh, s.Auth, publicKey, privateKey, payload)
		if gone {
			_ = db.DeletePushSubscription(database, s.Endpoint)
		}
		if sendErr != nil {
			failed++
		} else {
			delivered++
		}
	}
	return delivered, failed, nil
}
