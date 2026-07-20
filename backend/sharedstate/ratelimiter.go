package sharedstate

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// slidingWindowScript mirrors the original in-memory sliding-log rate
// limiter (backend/main.go's rateLimiter): drop hits older than the window,
// count what's left, and only record a new hit if still under the limit.
// Done as one atomic script so concurrent requests for the same key from
// different api replicas can't both slip through a check-then-increment
// race.
var slidingWindowScript = redis.NewScript(`
local key = KEYS[1]
local now_ms = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local cutoff = now_ms - window_ms

redis.call('ZREMRANGEBYSCORE', key, '-inf', cutoff)
local count = redis.call('ZCARD', key)
if count >= limit then
    redis.call('PEXPIRE', key, window_ms)
    return 0
end

local seq = redis.call('INCR', key .. ':seq')
redis.call('ZADD', key, now_ms, now_ms .. '-' .. seq)
redis.call('PEXPIRE', key, window_ms)
redis.call('PEXPIRE', key .. ':seq', window_ms)
return 1
`)

// RedisRateLimiter is a drop-in replacement for the in-memory rate limiter
// used by login/refresh/init/change-password endpoints, sharing counters
// across every api replica instead of one counter per process.
type RedisRateLimiter struct {
	client *Client
	bucket string
	limit  int
	window time.Duration
}

func NewRedisRateLimiter(client *Client, bucket string, limit int, window time.Duration) *RedisRateLimiter {
	return &RedisRateLimiter{client: client, bucket: bucket, limit: limit, window: window}
}

// Allow reports whether a request identified by key (typically the client
// IP) is within the configured limit for this bucket. On a Redis error it
// fails open (allows the request) and logs a warning - a rate limiter
// outage should not itself take down login/refresh/init, and those
// endpoints already depend on Postgres being reachable to do anything
// useful anyway.
func (rl *RedisRateLimiter) Allow(key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	redisKey := "ratelimit:" + rl.bucket + ":" + key
	now := time.Now().UnixMilli()

	res, err := slidingWindowScript.Run(ctx, rl.client.rdb, []string{redisKey}, now, rl.window.Milliseconds(), rl.limit).Int()
	if err != nil {
		slog.Warn("redis rate limiter error, failing open", "bucket", rl.bucket, "error", err)
		return true
	}
	return res == 1
}
