package control

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"syslytics/sharedstate"
)

const (
	redisOpTimeout     = 2 * time.Second
	pauseRefreshPeriod = 10 * time.Second
)

// IngestionController gates whether the log tailer is actively processing
// new lines. New reports two implementations behind this interface: a
// local, in-memory one (default, single-server/single-replica) and a
// Redis-backed one (when multiple api replicas are running - the pause
// flag needs to be visible to whichever replica currently holds tailer
// leadership, not just the replica that received the pause/resume request).
type IngestionController interface {
	IsPaused() bool
	Pause()
	Resume()
}

// New returns a Redis-backed controller when client is non-nil, otherwise
// the local in-memory controller (today's behavior, used by the
// single-server deployment and any replica running without Redis
// configured).
func New(ctx context.Context, client *sharedstate.Client) IngestionController {
	if client == nil {
		return newLocalController()
	}
	return newRedisController(ctx, client)
}

// ---- local: in-memory, single-process ----

type localController struct {
	mu     sync.RWMutex
	paused bool
}

func newLocalController() *localController {
	return &localController{}
}

func (c *localController) IsPaused() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.paused
}

func (c *localController) Pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused = true
}

func (c *localController) Resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused = false
}

// ---- redis-backed: shared across replicas ----

const (
	ingestionPauseKey       = "ingestion:paused"
	ingestionControlChannel = "ingestion:control"
)

// redisController keeps a locally-cached copy of the pause flag, updated
// via Redis pub/sub, so the tailer's hot path (checked multiple times per
// batch) is a plain atomic load rather than a network round trip on every
// check. The cache is seeded from Redis at construction and kept current by
// a background subscriber for the lifetime of ctx.
type redisController struct {
	client      *sharedstate.Client
	broadcaster *sharedstate.Broadcaster
	cached      atomic.Bool
	refreshCtx context.Context
	refreshCancel context.CancelFunc
}

func newRedisController(ctx context.Context, client *sharedstate.Client) *redisController {
	refreshCtx, cancel := context.WithCancel(context.Background())
	c := &redisController{
		client:         client,
		broadcaster:    sharedstate.NewBroadcaster(client),
		cached:         atomic.Bool{},
		refreshCtx:     refreshCtx,
		refreshCancel:  cancel,
	}
	c.cached.Store(c.readPaused(ctx))

	go c.broadcaster.Subscribe(ctx, ingestionControlChannel, func(payload string) {
		c.cached.Store(payload == "1")
	})

	go func() {
		ticker := time.NewTicker(pauseRefreshPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
				wasPaused := c.cached.Load()
				newPaused := c.readPaused(refreshCtx)
				if newPaused != wasPaused {
					slog.Info("ingestion: pause state drifted, corrected", "was", wasPaused, "now", newPaused)
				}
				c.cached.Store(newPaused)
			}
		}
	}()

	return c
}

func (c *redisController) readPaused(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()

	v, err := c.client.Raw().Get(ctx, ingestionPauseKey).Result()
	if err != nil {
		// Includes redis.Nil (key never set, i.e. not paused) as well as
		// real errors - either way, defaulting to "not paused" matches the
		// zero-value default of the local controller.
		return false
	}
	return v == "1"
}

func (c *redisController) IsPaused() bool {
	return c.cached.Load()
}

func (c *redisController) Pause() {
	c.setPaused(true)
}

func (c *redisController) Resume() {
	c.setPaused(false)
}

func (c *redisController) Close() {
	c.refreshCancel()
}

func (c *redisController) setPaused(paused bool) {
	c.cached.Store(paused)

	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()

	value := "0"
	if paused {
		value = "1"
	}
	if err := c.client.Raw().Set(ctx, ingestionPauseKey, value, 0).Err(); err != nil {
		return
	}
	if err := c.broadcaster.Publish(ctx, ingestionControlChannel, value); err != nil {
		slog.Warn("failed to broadcast ingestion state change", "paused", paused, "error", err)
	}
}
