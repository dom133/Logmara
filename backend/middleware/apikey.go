package middleware

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"logmara/db"
	"logmara/model"
	"logmara/sharedstate"
	"logmara/util"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

// hashAPIKey returns the digest of a plaintext API key for storage/lookup
// (see util.TokenHash: hex(HMAC-SHA256(TOKEN_HASH_KEY, key))). Must stay in
// sync with handler.hashAPIKey (the two packages can't share the function
// directly - handler already imports middleware).
func hashAPIKey(key string) string {
	return util.TokenHash(key)
}

type APIKeyPermissions struct {
	ExportJSON   bool `json:"export_json"`
	ExportParsed bool `json:"export_parsed"`
	ViewStats    bool `json:"view_stats"`
}

type ScopeFilters struct {
	Hostnames  []string `json:"hostnames"`
	Severities []string `json:"severities"`
	// MatchMode controls how the Hostnames and Severities conditions combine
	// when both are set: "and" (default) requires both, "or" requires either.
	// Within a single field, values always combine with OR (an IN clause) -
	// this only affects combining the two fields with each other.
	MatchMode string `json:"match_mode"`
}

func APIKeyAuth(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithError(http.StatusUnauthorized, model.NewUnauthorizedKey("unauthorized", "Missing Authorization header", nil))
			return
		}

		var apiKey string
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			apiKey = authHeader[7:]
		} else {
			c.AbortWithError(http.StatusUnauthorized, model.NewUnauthorizedKey("unauthorized", "Invalid Authorization format", nil))
			return
		}

		if len(apiKey) < 8 {
			c.AbortWithError(http.StatusUnauthorized, model.NewUnauthorizedKey("unauthorized", "Invalid API key", nil))
			return
		}

		keyPrefix := apiKey[:8]
		// New API keys are stored as hex(HMAC-SHA256(TOKEN_HASH_KEY, key));
		// keys created before the HMAC scheme existed are stored as unsalted
		// SHA-256 (their plaintext is unrecoverable, so they can't be
		// re-hashed). Accept both.
		keyHash := hashAPIKey(apiKey)
		legacyHash := util.TokenHashLegacy(apiKey)

		database := pool.Get()
		row := database.QueryRow(`
			SELECT id, key_hash, is_active, permissions, scope_filters, rate_limit_per_min, expires_at, allowed_ips
			FROM api_keys
			WHERE key_hash = $1 OR key_hash = $2
		`, keyHash, legacyHash)

		var id int
		var storedHash string
		var isActive bool
		var permissionsJSON, scopeFiltersJSON []byte
		var rateLimitPerMin int
		var expiresAt sql.NullTime
		var allowedIPs pq.StringArray

		err := row.Scan(&id, &storedHash, &isActive, &permissionsJSON, &scopeFiltersJSON, &rateLimitPerMin, &expiresAt, &allowedIPs)
		if err != nil {
			c.AbortWithError(http.StatusUnauthorized, model.NewUnauthorizedKey("unauthorized", "Invalid API key", nil))
			return
		}

		if !isActive {
			c.AbortWithError(http.StatusUnauthorized, model.NewUnauthorizedKey("unauthorized", "API key is inactive", nil))
			return
		}

		if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
			c.AbortWithError(http.StatusUnauthorized, model.NewUnauthorizedKey("unauthorized", "API key has expired", nil))
			return
		}

		if len(allowedIPs) > 0 && !ipAllowed(c.ClientIP(), allowedIPs) {
			c.AbortWithError(http.StatusForbidden, model.NewForbiddenKey("ip_not_allowed", "API key is not permitted from this IP address", nil))
			return
		}

		var perms APIKeyPermissions
		if err := json.Unmarshal(permissionsJSON, &perms); err != nil {
			c.AbortWithError(http.StatusUnauthorized, model.NewUnauthorizedKey("unauthorized", "Invalid API key permissions", nil))
			return
		}

		var filters *ScopeFilters
		if scopeFiltersJSON != nil {
			filters = &ScopeFilters{}
			if err := json.Unmarshal(scopeFiltersJSON, filters); err != nil {
				slog.Error("Failed to parse scope filters", "key_id", id, "error", err)
			}
		}

		limiter := getRateLimiter(id, rateLimitPerMin)
		if !limiter.allow() {
			c.AbortWithError(http.StatusTooManyRequests, model.NewTooManyRequestsKey("rate_limited", "Rate limit exceeded", nil))
			return
		}

		database.Exec(`UPDATE api_keys SET last_used_at = NOW(), total_requests = total_requests + 1 WHERE id = $1`, id)

		c.Set("api_key_permissions", perms)
		if filters != nil {
			c.Set("scope_filters", filters)
		}
		c.Set("api_key_id", id)
		c.Set("api_key_prefix", keyPrefix)

		c.Next()
	}
}

var (
	rateLimitersMu   sync.Mutex
	rateLimiters     = make(map[int]keyRateLimiter)
	sharedRateClient *sharedstate.Client
)

// keyRateLimiter enforces a single API key's per-minute request limit, backed
// either by local in-memory state (single replica) or shared Redis (HA), so
// the limit is enforced consistently across every api replica instead of one
// counter per process.
type keyRateLimiter interface {
	allow() bool
}

// memoryKeyLimiter wraps the per-process token bucket.
type memoryKeyLimiter struct {
	rl *RateLimiter
}

func (m *memoryKeyLimiter) allow() bool { return m.rl.Allow() }

// redisKeyLimiter wraps the shared Redis sliding-window limiter; the key is
// the API key's id, so each key gets its own counter shared across replicas.
type redisKeyLimiter struct {
	rl  *sharedstate.RedisRateLimiter
	key string
}

func (r *redisKeyLimiter) allow() bool { return r.rl.Allow(r.key) }

// SetSharedRateClient configures the shared (Redis-backed) rate limiter used
// for API key limits when running multiple api replicas over Redis. Nil (the
// default, single-server) falls back to a per-process in-memory limiter.
func SetSharedRateClient(client *sharedstate.Client) {
	rateLimitersMu.Lock()
	defer rateLimitersMu.Unlock()
	sharedRateClient = client
}

type RateLimiter struct {
	mu       sync.Mutex
	tokens   int
	maxToken int
	stop     chan struct{}
}

func NewRateLimiter(maxTokens int) *RateLimiter {
	rl := &RateLimiter{
		tokens:   maxTokens,
		maxToken: maxTokens,
		stop:     make(chan struct{}),
	}
	go rl.refill()
	return rl
}

func (rl *RateLimiter) refill() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			rl.tokens = rl.maxToken
			rl.mu.Unlock()
		case <-rl.stop:
			return
		}
	}
}

func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.tokens > 0 {
		rl.tokens--
		return true
	}
	return false
}

func getRateLimiter(keyID int, rateLimit int) keyRateLimiter {
	rateLimitersMu.Lock()
	defer rateLimitersMu.Unlock()
	if rl, ok := rateLimiters[keyID]; ok {
		return rl
	}
	var rl keyRateLimiter
	if sharedRateClient != nil {
		rl = &redisKeyLimiter{
			rl:  sharedstate.NewRedisRateLimiter(sharedRateClient, "apikey", rateLimit, time.Minute),
			key: strconv.Itoa(keyID),
		}
	} else {
		rl = &memoryKeyLimiter{rl: NewRateLimiter(rateLimit)}
	}
	rateLimiters[keyID] = rl
	return rl
}

// RemoveRateLimiter drops a key's limiter entry so deleted API keys don't
// accumulate leaked limiters/goroutines forever. For the in-memory variant it
// also stops the background refill goroutine; the Redis-backed variant has no
// local state to release. Safe to call even if no limiter was created for keyID.
func RemoveRateLimiter(keyID int) {
	rateLimitersMu.Lock()
	defer rateLimitersMu.Unlock()
	if m, ok := rateLimiters[keyID].(*memoryKeyLimiter); ok {
		close(m.rl.stop)
	}
	delete(rateLimiters, keyID)
}

// ipAllowed reports whether clientIP matches any entry in allowed - each
// entry is either a single IP ("10.0.0.5") or a CIDR range ("10.0.0.0/24").
// Malformed entries and an unparsable clientIP never match (fail closed).
func ipAllowed(clientIP string, allowed []string) bool {
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}
	for _, entry := range allowed {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, ipNet, err := net.ParseCIDR(entry)
			if err == nil && ipNet.Contains(ip) {
				return true
			}
			continue
		}
		if entryIP := net.ParseIP(entry); entryIP != nil && entryIP.Equal(ip) {
			return true
		}
	}
	return false
}

func CheckPermission(c *gin.Context, perm string) bool {
	perms, ok := c.Get("api_key_permissions")
	if !ok {
		return false
	}
	p := perms.(APIKeyPermissions)
	switch perm {
	case "export_json":
		return p.ExportJSON
	case "export_parsed":
		return p.ExportParsed
	case "view_stats":
		return p.ViewStats
	}
	return false
}

func ApplyScopeFilters(c *gin.Context, query string, args *[]any) (string, []any) {
	filters, exists := c.Get("scope_filters")
	if !exists {
		return query, *args
	}
	f := filters.(*ScopeFilters)
	conds := []string{}
	if len(f.Hostnames) > 0 {
		placeholders := make([]string, len(f.Hostnames))
		vals := make([]any, len(f.Hostnames))
		for i, h := range f.Hostnames {
			placeholders[i] = "$" + strconv.Itoa(len(*args)+i+1)
			vals[i] = h
		}
		conds = append(conds, "host_address IN ("+strings.Join(placeholders, ",")+")")
		*args = append(*args, vals...)
	}
	if len(f.Severities) > 0 {
		placeholders := make([]string, len(f.Severities))
		vals := make([]any, len(f.Severities))
		for i, s := range f.Severities {
			placeholders[i] = "$" + strconv.Itoa(len(*args)+i+1)
			vals[i] = s
		}
		conds = append(conds, "severity IN ("+strings.Join(placeholders, ",")+")")
		*args = append(*args, vals...)
	}
	if len(conds) > 0 {
		joiner := " AND "
		if f.MatchMode == "or" {
			joiner = " OR "
		}
		if query != "" {
			query += " AND "
		}
		query += "(" + strings.Join(conds, joiner) + ")"
	}
	return query, *args
}
