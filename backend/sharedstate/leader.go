package sharedstate

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// renewScript / releaseScript only act if the calling instance still owns
// the lock (its id matches what's stored) - this is the standard
// compare-and-{expire,delete} pattern for a Redis-backed distributed lock,
// preventing an instance that lost and reacquired ownership from
// unexpectedly resetting a lock now held by someone else.
var renewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('PEXPIRE', KEYS[1], ARGV[2])
else
    return 0
end
`)

var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
else
    return 0
end
`)

// LeaderElector implements single-leader election via a Redis key with a
// TTL: whoever successfully SETs it (NX) is the leader until it either
// releases the key or fails to renew it before the TTL expires (e.g. it
// crashed, or lost its Redis connection during a Sentinel failover) - at
// which point Redis expires the key on its own and any other instance can
// acquire it. There is no separate lease-renewal daemon here; callers are
// expected to call Renew periodically (well inside ttl) while they believe
// they're still leader.
type LeaderElector struct {
	client *Client
	key    string
	id     string
	ttl    time.Duration
}

func NewLeaderElector(client *Client, key string, ttl time.Duration) *LeaderElector {
	return &LeaderElector{client: client, key: "leader:" + key, id: uuid.NewString(), ttl: ttl}
}

// Acquire attempts to become leader. Returns false if another instance
// already holds the lock.
func (le *LeaderElector) Acquire(ctx context.Context) bool {
	ok, err := le.client.rdb.SetNX(ctx, le.key, le.id, le.ttl).Result()
	if err != nil {
		slog.Warn("leader election acquire error", "key", le.key, "error", err)
		return false
	}
	return ok
}

// Renew extends the lock's TTL. ok reports whether this instance is still
// leader. lost distinguishes *why* it isn't, which callers must treat very
// differently:
//
//   - lost=true: the script ran and definitively confirmed this instance no
//     longer owns the key (TTL expiry + another instance's Acquire already
//     raced ahead). Someone else may already be acting as leader, so the
//     caller must stop acting as leader immediately - tolerating this for
//     even a few more seconds risks two instances running concurrently.
//   - lost=false (with ok=false): a transient error talking to Redis
//     (network blip, timeout, Sentinel failover in progress). This
//     instance's leadership status is simply unknown, not disproven - safe
//     for callers to tolerate a handful of consecutive occurrences before
//     stepping down, since nothing indicates anyone else has taken over.
func (le *LeaderElector) Renew(ctx context.Context) (ok bool, lost bool) {
	res, err := renewScript.Run(ctx, le.client.rdb, []string{le.key}, le.id, le.ttl.Milliseconds()).Int()
	if err != nil {
		slog.Warn("leader election renew error", "key", le.key, "error", err)
		return false, false
	}
	if res == 1 {
		return true, false
	}
	return false, true
}

// Release voluntarily gives up leadership (e.g. on clean shutdown) so
// another instance doesn't have to wait out the full TTL.
func (le *LeaderElector) Release(ctx context.Context) {
	if _, err := releaseScript.Run(ctx, le.client.rdb, []string{le.key}, le.id).Result(); err != nil {
		slog.Warn("leader election release error", "key", le.key, "error", err)
	}
}
