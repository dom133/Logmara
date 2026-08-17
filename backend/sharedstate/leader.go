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

// Acquire attempts to become leader. Retries up to 3 times on transient
// Redis errors (timeout, network blip, Sentinel failover) with a 1s delay
// between attempts. Returns false if another instance already holds the lock.
func (le *LeaderElector) Acquire(ctx context.Context) bool {
	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			slog.Warn("leader election: retrying acquire", "key", le.key, "attempt", attempt+1, "max", maxAttempts)
			select {
			case <-ctx.Done():
				return false
			case <-time.After(1 * time.Second):
			}
		}

		ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
		ok, err := le.client.Raw().SetNX(ctx2, le.key, le.id, le.ttl).Result()
		cancel()

		if err != nil {
			slog.Warn("leader election acquire error", "key", le.key, "error", err, "attempt", attempt+1)
			continue
		}
		return ok
	}
	return false
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := renewScript.Run(ctx, le.client.Raw(), []string{le.key}, le.id, le.ttl.Milliseconds()).Int()
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := releaseScript.Run(ctx, le.client.Raw(), []string{le.key}, le.id).Result(); err != nil {
		slog.Warn("leader election release error", "key", le.key, "error", err)
	}
}

// LeadershipOpts configures a RunLeadership loop.
type LeadershipOpts struct {
	// Tick is the interval between renewal attempts (while leader) and
	// acquisition retries (while not leader).
	Tick time.Duration
	// MaxTransientFails is how many consecutive transient renewal failures
	// are tolerated before stepping down. Beyond that count the lock TTL has
	// almost certainly run out and another instance may already be leading,
	// so continuing would risk two concurrent leaders.
	MaxTransientFails int
	// OnBecomeLeader is called once when this instance takes over the lock,
	// with a context that is cancelled on step-down (use it to run the
	// leader-only goroutines).
	OnBecomeLeader func(leaderCtx context.Context)
	// OnStepDown is called once when this instance stops leading (lost lock,
	// TTL exhaustion, or shutdown).
	OnStepDown func()
}

// RunLeadership acquires the lock and keeps it for the lifetime of ctx:
// while leader it renews the TTL every Tick, and while not leader it retries
// Acquire every Tick, so a dead leader is replaced automatically once its
// TTL expires. Stepping down happens on a definitive loss (Renew lost=true),
// after MaxTransientFails consecutive transient renew failures, or on ctx
// cancellation (which also releases the lock if still held). Blocks until
// ctx is done.
func (le *LeaderElector) RunLeadership(ctx context.Context, opts LeadershipOpts) {
	if opts.Tick <= 0 {
		opts.Tick = 30 * time.Second
	}
	if opts.MaxTransientFails <= 0 {
		opts.MaxTransientFails = 2
	}

	var leaderCancel context.CancelFunc

	stepDown := func() {
		if leaderCancel != nil {
			leaderCancel()
			leaderCancel = nil
		}
		if opts.OnStepDown != nil {
			opts.OnStepDown()
		}
	}

	becomeLeader := func() {
		leaderCtx, cancel := context.WithCancel(ctx)
		leaderCancel = cancel
		if opts.OnBecomeLeader != nil {
			opts.OnBecomeLeader(leaderCtx)
		}
	}

	isLeader := func() bool { return leaderCancel != nil }

	if le.Acquire(ctx) {
		becomeLeader()
	}

	ticker := time.NewTicker(opts.Tick)
	defer ticker.Stop()
	transientFails := 0
	for {
		select {
		case <-ctx.Done():
			if isLeader() {
				le.Release(context.Background())
			}
			stepDown()
			return
		case <-ticker.C:
			if !isLeader() {
				if le.Acquire(ctx) {
					becomeLeader()
				}
				continue
			}
			ok, lost := le.Renew(ctx)
			switch {
			case ok:
				transientFails = 0
			case lost:
				stepDown()
			default:
				transientFails++
				if transientFails >= opts.MaxTransientFails {
					stepDown()
				}
			}
		}
	}
}
