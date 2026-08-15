package db

import (
	"database/sql"
	"fmt"

	webpush "github.com/SherClockHolmes/webpush-go"

	"syslog-gui/model"
)

// GetOrCreateVAPIDKeys returns the VAPID key pair used to sign Web Push
// requests, generating and persisting one on first use. The ON CONFLICT DO
// NOTHING + re-read handles the race between multiple api replicas both
// finding no keys and generating their own at the same time - whichever
// insert wins, every replica ends up reading the same pair back.
func GetOrCreateVAPIDKeys(db *sql.DB) (publicKey, privateKey string, err error) {
	publicKey = getSettingRaw(db, "vapid_public_key")
	privateKey = getSettingRaw(db, "vapid_private_key")
	if publicKey != "" && privateKey != "" {
		return publicKey, privateKey, nil
	}

	newPrivate, newPublic, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", "", fmt.Errorf("generate VAPID keys: %w", err)
	}

	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ('vapid_public_key', $1) ON CONFLICT (key) DO NOTHING`, newPublic); err != nil {
		return "", "", fmt.Errorf("save VAPID public key: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ('vapid_private_key', $1) ON CONFLICT (key) DO NOTHING`, newPrivate); err != nil {
		return "", "", fmt.Errorf("save VAPID private key: %w", err)
	}

	publicKey = getSettingRaw(db, "vapid_public_key")
	privateKey = getSettingRaw(db, "vapid_private_key")
	if publicKey == "" || privateKey == "" {
		return "", "", fmt.Errorf("VAPID keys missing after insert")
	}
	return publicKey, privateKey, nil
}

// SavePushSubscription upserts a browser's push registration. Endpoint is
// unique per browser/device, so re-subscribing (e.g. after logging in as a
// different user on a shared browser) just re-points it at the new user.
func SavePushSubscription(db *sql.DB, userID int64, endpoint, p256dh, auth string) error {
	_, err := db.Exec(
		`INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (endpoint) DO UPDATE SET user_id=$1, p256dh=$3, auth=$4`,
		userID, endpoint, p256dh, auth,
	)
	return err
}

// DeletePushSubscription removes a browser's push registration, e.g. when
// the user disables push or the endpoint reports itself expired.
func DeletePushSubscription(db *sql.DB, endpoint string) error {
	_, err := db.Exec(`DELETE FROM push_subscriptions WHERE endpoint = $1`, endpoint)
	return err
}

// GetAllPushSubscriptions returns every registered push subscription -
// push notifications are delivered to every subscribed browser, the same
// way in-app notifications go to every signed-in user.
func GetAllPushSubscriptions(db *sql.DB) ([]model.PushSubscription, error) {
	rows, err := db.Query(`SELECT id, user_id, endpoint, p256dh, auth, created_at FROM push_subscriptions ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []model.PushSubscription
	for rows.Next() {
		var s model.PushSubscription
		if err := rows.Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth, &s.CreatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, s)
	}
	return subs, rows.Err()
}
