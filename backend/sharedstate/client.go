// Package sharedstate provides distributed coordination primitives (rate
// limiting, pub/sub broadcast, leader election) backed by Redis/Redis
// Sentinel, for deployments running more than one instance of the api
// service behind a load balancer.
//
// It is entirely optional: Connect returns (nil, nil) when no REDIS_*
// env vars are set, and every caller in this codebase falls back to its
// original single-process, in-memory behavior in that case. Single-server
// deployments (docker-compose.yml) never set these vars and are therefore
// completely unaffected by this package.
package sharedstate

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"logmara/util"

	"github.com/redis/go-redis/v9"
)

// Client wraps a Redis connection that may be a plain client or a
// Sentinel-aware failover client, depending on configuration.
// The rdb field is an atomic.Value so RotatePassword can swap the
// underlying connection without tearing down every consumer.
type Client struct {
	rdb atomic.Value // redis.UniversalClient
}

// Raw exposes the underlying go-redis client for callers that need direct
// command access (e.g. LPUSH/LTRIM for a domain-specific list) rather than
// one of this package's higher-level primitives.
func (c *Client) Raw() redis.UniversalClient {
	return c.rdb.Load().(redis.UniversalClient)
}

func (c *Client) Close() error {
	rdb := c.rdb.Load().(redis.UniversalClient)
	return rdb.Close()
}

// RotatePassword closes the current Redis client, creates a new one with
// the updated password, verifies connectivity, and atomically swaps it in.
// Downtime is ~50-100 ms (close + connect + ping + swap). All existing
// callers that read via Raw() will transparently get the new client.
func (c *Client) RotatePassword(newPassword string) error {
	oldRDB := c.rdb.Load().(redis.UniversalClient)
	if oldRDB == nil {
		return fmt.Errorf("redis: no client loaded, cannot rotate")
	}

	newRDB, err := c.cloneClientWithPassword(newPassword)
	if err != nil {
		slog.Warn("redis: password rotation failed, keeping existing client", "error", err)
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := newRDB.Ping(ctx).Err(); err != nil {
		_ = newRDB.Close()
		slog.Warn("redis: new client ping failed during rotation, keeping existing client", "error", err)
		return err
	}

	oldRDB.Close()
	c.rdb.Store(newRDB)
	slog.Info("redis: password rotated successfully")
	return nil
}

// cloneClientWithPassword creates a new Redis client based on the current
// client's configuration but with an updated password.
func (c *Client) cloneClientWithPassword(newPassword string) (redis.UniversalClient, error) {
	current := c.rdb.Load().(redis.UniversalClient)
	if current == nil {
		return nil, fmt.Errorf("redis: no client loaded")
	}

	sentinelAddrsRaw := strings.TrimSpace(os.Getenv("REDIS_SENTINEL_ADDRS"))
	addr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))

	switch {
	case sentinelAddrsRaw != "":
		masterName := strings.TrimSpace(os.Getenv("REDIS_MASTER_NAME"))
		if masterName == "" {
			masterName = "mymaster"
		}
		return redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:       masterName,
			SentinelAddrs:    splitAndTrim(sentinelAddrsRaw),
			Password:         newPassword,
			SentinelPassword: newPassword,
			DialTimeout:      5 * time.Second,
			ReadTimeout:      5 * time.Second,
			WriteTimeout:     5 * time.Second,
		}), nil
	case addr != "":
		return redis.NewClient(&redis.Options{
			Addr:         addr,
			Password:     newPassword,
			DialTimeout:  5 * time.Second,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
		}), nil
	default:
		return nil, fmt.Errorf("redis: no Redis address configured")
	}
}

// Connect reads Redis configuration from the environment:
//
//   - REDIS_SENTINEL_ADDRS: comma-separated sentinel host:port list. When
//     set, connects via Sentinel (REDIS_MASTER_NAME, default "mymaster").
//   - REDIS_ADDR: a single host:port, used when REDIS_SENTINEL_ADDRS is
//     unset (simple non-Sentinel setups, e.g. local dev/testing).
//   - REDIS_PASSWORD (or REDIS_PASSWORD_FILE): optional, used for both the
//     data connection and (in Sentinel mode) sentinel auth.
//
// Returns (nil, nil) when neither REDIS_SENTINEL_ADDRS nor REDIS_ADDR is
// set - this is the expected, silent "not configured" path for
// single-server deployments. Returns a non-nil error when Redis *is*
// configured but unreachable: running multiple api replicas without working
// coordination is actively unsafe (duplicate log ingestion, multiplied rate
// limits), so that case should fail startup rather than silently degrading
// to single-process behavior.
func Connect() (*Client, error) {
	sentinelAddrsRaw := strings.TrimSpace(os.Getenv("REDIS_SENTINEL_ADDRS"))
	addr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	// Supports REDIS_PASSWORD_FILE (a mounted Docker/Swarm secret) as well as
	// the plain env var - see util.SecretFromEnv.
	password := util.SecretFromEnv("REDIS_PASSWORD")

	var rdb redis.UniversalClient
	switch {
	case sentinelAddrsRaw != "":
		masterName := strings.TrimSpace(os.Getenv("REDIS_MASTER_NAME"))
		if masterName == "" {
			masterName = "mymaster"
		}
		rdb = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:       masterName,
			SentinelAddrs:    splitAndTrim(sentinelAddrsRaw),
			Password:         password,
			SentinelPassword: password,
			DialTimeout:      5 * time.Second,
			ReadTimeout:      5 * time.Second,
			WriteTimeout:     5 * time.Second,
		})
	case addr != "":
		rdb = redis.NewClient(&redis.Options{
			Addr:         addr,
			Password:     password,
			DialTimeout:  5 * time.Second,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
		})
	default:
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis configured but unreachable: %w", err)
	}

	client := &Client{}
	client.rdb.Store(rdb)
	return client, nil
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
