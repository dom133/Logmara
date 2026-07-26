// Package notifyhub fans in-app notifications out to connected browser
// clients over SSE. With Redis configured it publishes through the shared
// broadcaster so every api replica's connected clients receive it, not just
// the replica that happened to handle the triggering request.
package notifyhub

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"logmara/model"
	"logmara/sharedstate"
)

const channelName = "notifications:new"

type Hub struct {
	mu          sync.Mutex
	clients     map[chan model.InAppNotification]struct{}
	broadcaster *sharedstate.Broadcaster
}

// NewHub builds a Hub. client may be nil (single-server deployments without
// Redis), in which case Publish only fans out to this process's own
// connected clients.
func NewHub(ctx context.Context, client *sharedstate.Client) *Hub {
	h := &Hub{clients: make(map[chan model.InAppNotification]struct{})}
	if client != nil {
		h.broadcaster = sharedstate.NewBroadcaster(client)
		go h.broadcaster.Subscribe(ctx, channelName, func(payload string) {
			var n model.InAppNotification
			if err := json.Unmarshal([]byte(payload), &n); err != nil {
				slog.Warn("notifyhub: dropping malformed message", "error", err)
				return
			}
			h.fanOutLocal(n)
		})
	}
	return h
}

// Publish fans n out to connected clients. With Redis configured, this
// publishes to every replica (including this one, via its own subscription
// above) instead of fanning out locally itself, so each notification is
// delivered exactly once per client regardless of which replica produced it.
func (h *Hub) Publish(n model.InAppNotification) {
	if h.broadcaster == nil {
		h.fanOutLocal(n)
		return
	}

	b, err := json.Marshal(n)
	if err != nil {
		slog.Warn("notifyhub: failed to marshal notification", "error", err)
		h.fanOutLocal(n)
		return
	}
	if err := h.broadcaster.Publish(context.Background(), channelName, string(b)); err != nil {
		slog.Warn("notifyhub: publish failed, falling back to local fan-out", "error", err)
		h.fanOutLocal(n)
	}
}

func (h *Hub) fanOutLocal(n model.InAppNotification) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- n:
		default:
			// Slow/stuck client - drop rather than block the publisher.
		}
	}
}

// Subscribe registers a new client and returns the channel it should read
// from until the request ends, at which point call Unsubscribe with it.
func (h *Hub) Subscribe() chan model.InAppNotification {
	ch := make(chan model.InAppNotification, 8)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) Unsubscribe(ch chan model.InAppNotification) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}
