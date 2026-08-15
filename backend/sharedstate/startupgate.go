package sharedstate

import (
	"context"
	"log/slog"
	"time"
)

const (
	// startupSetKey holds one member per api replica that has reached the
	// WaitForReplicas call site, keyed by that replica's identity (see
	// main.go's SWARM_TASK_IDENTITY/os.Hostname fallback). A Set rather than
	// a counter so a replica calling this twice (e.g. a process restart
	// reusing the same Swarm task slot) never double-counts.
	startupSetKey = "startup:api:instances"
	// startupSetTTL bounds how long members from a previous `docker stack
	// deploy`/rescale can linger - long enough to cover a slow rolling
	// update, short enough that a stale set from days ago can't silently
	// satisfy a future deploy's replica count.
	startupSetTTL       = 10 * time.Minute
	startupPollInterval = 1 * time.Second
)

// WaitForReplicas registers this replica's instanceID as started and blocks
// until at least `expected` replicas have done the same, or until timeout
// elapses - whichever comes first. It's a no-op when client is nil (Redis
// not configured) or expected <= 1 (single-replica deployments), matching
// the rest of this package's fallback convention.
//
// Deliberately bounded rather than waiting forever: a sibling replica that
// never starts (crash-looping on a bad config, failed image pull) must not
// also prevent this replica from ever ingesting logs - on timeout this logs
// a warning and proceeds with however many replicas actually registered.
func WaitForReplicas(ctx context.Context, client *Client, instanceID string, expected int, timeout time.Duration) {
	if client == nil || expected <= 1 {
		return
	}

	rdb := client.Raw()

	regCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.SAdd(regCtx, startupSetKey, instanceID).Err(); err != nil {
		slog.Warn("startup barrier: failed to register this replica, proceeding without waiting", "error", err)
		return
	}
	if err := rdb.Expire(regCtx, startupSetKey, startupSetTTL).Err(); err != nil {
		slog.Warn("startup barrier: failed to set expiry on registration set", "error", err)
	}

	slog.Info("startup barrier: waiting for all api replicas to start before starting tailer", "instance", instanceID, "expected", expected, "timeout", timeout)

	deadline := time.Now().Add(timeout)
	for {
		count, err := rdb.SCard(ctx, startupSetKey).Result()
		if err != nil {
			slog.Warn("startup barrier: failed to read registered replica count, proceeding", "error", err)
			return
		}
		if count >= int64(expected) {
			slog.Info("startup barrier: all api replicas started, starting tailer", "registered", count)
			return
		}
		if time.Now().After(deadline) {
			slog.Warn("startup barrier: timed out waiting for all api replicas to start, starting tailer anyway", "registered", count, "expected", expected)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(startupPollInterval):
		}
	}
}
