package sharedstate

import (
	"context"
)

// Broadcaster is a thin wrapper around Redis pub/sub, used to propagate
// events (cache invalidation, ingestion pause/resume) to every api replica
// instead of just the one that handled the triggering request.
type Broadcaster struct {
	client *Client
}

func NewBroadcaster(client *Client) *Broadcaster {
	return &Broadcaster{client: client}
}

func (b *Broadcaster) Publish(ctx context.Context, channel, payload string) error {
	return b.client.Raw().Publish(ctx, channel, payload).Err()
}

// Subscribe blocks the calling goroutine until ctx is done, invoking
// onMessage for every message received on channel. Callers should run this
// in its own goroutine. go-redis's PubSub transparently reconnects (e.g.
// across a Sentinel failover), so this does not need its own retry loop.
func (b *Broadcaster) Subscribe(ctx context.Context, channel string, onMessage func(payload string)) {
	pubsub := b.client.Raw().Subscribe(ctx, channel)
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			onMessage(msg.Payload)
		}
	}
}
