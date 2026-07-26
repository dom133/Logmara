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
	"os"
	"strings"
	"time"

	"syslytics/util"

	"github.com/redis/go-redis/v9"
)

// Client wraps a Redis connection that may be a plain client or a
// Sentinel-aware failover client, depending on configuration.
type Client struct {
	rdb redis.UniversalClient
}

// Raw exposes the underlying go-redis client for callers that need direct
// command access (e.g. LPUSH/LTRIM for a domain-specific list) rather than
// one of this package's higher-level primitives.
func (c *Client) Raw() redis.UniversalClient {
	return c.rdb
}

func (c *Client) Close() error {
	return c.rdb.Close()
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
		})
	case addr != "":
		rdb = redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: password,
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

	return &Client{rdb: rdb}, nil
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
