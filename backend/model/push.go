package model

import "time"

// PushSubscription is a browser's Web Push registration, created via the
// Push API on the client and used to deliver alert notifications even when
// the app isn't open.
type PushSubscription struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Endpoint  string    `json:"endpoint"`
	P256dh    string    `json:"-"`
	Auth      string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

type PushSubscribeRequest struct {
	Endpoint string `json:"endpoint" binding:"required"`
	Keys     struct {
		P256dh string `json:"p256dh" binding:"required"`
		Auth   string `json:"auth" binding:"required"`
	} `json:"keys" binding:"required"`
}

type PushUnsubscribeRequest struct {
	Endpoint string `json:"endpoint" binding:"required"`
}
