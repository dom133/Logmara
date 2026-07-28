package sharedstate

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestClient(t *testing.T) (*Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return &Client{rdb: rdb}, mr
}

func TestRedisRateLimiter_AllowsUpToLimitThenDenies(t *testing.T) {
	client, _ := newTestClient(t)
	rl := NewRedisRateLimiter(client, "test", 3, time.Minute)

	for i := 0; i < 3; i++ {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("request %d should have been allowed", i+1)
		}
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("4th request should have been denied, limit is 3")
	}
}

func TestRedisRateLimiter_PerKeyIsolation(t *testing.T) {
	client, _ := newTestClient(t)
	rl := NewRedisRateLimiter(client, "test", 1, time.Minute)

	if !rl.Allow("1.1.1.1") {
		t.Fatal("first request for 1.1.1.1 should be allowed")
	}
	if rl.Allow("1.1.1.1") {
		t.Fatal("second request for 1.1.1.1 should be denied")
	}
	if !rl.Allow("2.2.2.2") {
		t.Fatal("first request for a different key should still be allowed")
	}
}

func TestRedisRateLimiter_WindowExpiryAllowsAgain(t *testing.T) {
	client, mr := newTestClient(t)
	rl := NewRedisRateLimiter(client, "test", 1, time.Minute)

	if !rl.Allow("1.2.3.4") {
		t.Fatal("first request should be allowed")
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("second request within the window should be denied")
	}

	mr.FastForward(61 * time.Second)

	if !rl.Allow("1.2.3.4") {
		t.Fatal("request after the window elapsed should be allowed again")
	}
}

func TestLeaderElector_SecondInstanceCannotAcquireWhileFirstHolds(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	leaderA := NewLeaderElector(client, "tailer", 10*time.Second)
	leaderB := NewLeaderElector(client, "tailer", 10*time.Second)

	if !leaderA.Acquire(ctx) {
		t.Fatal("leaderA should acquire the uncontested lock")
	}
	if leaderB.Acquire(ctx) {
		t.Fatal("leaderB should not acquire a lock already held by leaderA")
	}
}

func TestLeaderElector_RenewFailsForNonOwner(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	leaderA := NewLeaderElector(client, "tailer", 10*time.Second)
	leaderB := NewLeaderElector(client, "tailer", 10*time.Second)

	if !leaderA.Acquire(ctx) {
		t.Fatal("leaderA should acquire the uncontested lock")
	}
	if ok, lost := leaderB.Renew(ctx); ok || !lost {
		t.Fatalf("leaderB renewing a lock it does not own: got ok=%v lost=%v, want ok=false lost=true (definitive)", ok, lost)
	}
	if ok, _ := leaderA.Renew(ctx); !ok {
		t.Fatal("leaderA should be able to renew its own lock")
	}
}

// TestLeaderElector_RenewIsDefinitiveLossAfterTakeover is the regression
// test for the swarm double-ingestion bug: when leaderA's TTL lapses (e.g. a
// missed renewal) and leaderB acquires in the gap, leaderA's next Renew must
// report a *definitive* loss (lost=true), not just ok=false - the tailer's
// runWithLeaderElection relies on that distinction to step down immediately
// instead of tolerating a few more renew cycles, which would otherwise let
// both instances tail the same file concurrently for several seconds.
func TestLeaderElector_RenewIsDefinitiveLossAfterTakeover(t *testing.T) {
	client, mr := newTestClient(t)
	ctx := context.Background()

	leaderA := NewLeaderElector(client, "tailer", 5*time.Second)
	leaderB := NewLeaderElector(client, "tailer", 5*time.Second)

	if !leaderA.Acquire(ctx) {
		t.Fatal("leaderA should acquire the uncontested lock")
	}

	// Simulate leaderA missing its renewal window (e.g. a network blip) long
	// enough for the TTL to lapse, then leaderB winning the race to acquire.
	mr.FastForward(6 * time.Second)
	if !leaderB.Acquire(ctx) {
		t.Fatal("leaderB should acquire the lock once leaderA's TTL has expired")
	}

	ok, lost := leaderA.Renew(ctx)
	if ok {
		t.Fatal("leaderA should not be able to renew a lock leaderB now owns")
	}
	if !lost {
		t.Fatal("leaderA's renew must be reported as a definitive loss (lost=true), not a transient failure - callers must step down immediately rather than tolerating retries")
	}
}

func TestLeaderElector_ReleaseThenReacquireByAnother(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	leaderA := NewLeaderElector(client, "tailer", 10*time.Second)
	leaderB := NewLeaderElector(client, "tailer", 10*time.Second)

	if !leaderA.Acquire(ctx) {
		t.Fatal("leaderA should acquire the uncontested lock")
	}
	leaderA.Release(ctx)
	if !leaderB.Acquire(ctx) {
		t.Fatal("leaderB should acquire the lock after leaderA released it")
	}
}

func TestLeaderElector_ExpiryAllowsAnotherToAcquire(t *testing.T) {
	client, mr := newTestClient(t)
	ctx := context.Background()

	leaderA := NewLeaderElector(client, "tailer", 5*time.Second)
	leaderB := NewLeaderElector(client, "tailer", 5*time.Second)

	if !leaderA.Acquire(ctx) {
		t.Fatal("leaderA should acquire the uncontested lock")
	}
	// Simulate leaderA crashing without releasing: no more renewals, just
	// let the TTL lapse.
	mr.FastForward(6 * time.Second)

	if !leaderB.Acquire(ctx) {
		t.Fatal("leaderB should acquire the lock once leaderA's TTL has expired")
	}
}

func TestBroadcaster_PublishSubscribe(t *testing.T) {
	client, _ := newTestClient(t)
	b := NewBroadcaster(client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan string, 1)
	go b.Subscribe(ctx, "test-channel", func(payload string) {
		received <- payload
	})

	// Give the subscriber goroutine time to actually subscribe before
	// publishing - miniredis pub/sub is synchronous enough in practice that
	// a short wait is sufficient here rather than needing a ready signal.
	time.Sleep(50 * time.Millisecond)

	if err := b.Publish(ctx, "test-channel", "hello"); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case msg := <-received:
		if msg != "hello" {
			t.Fatalf("expected 'hello', got %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published message")
	}
}
