package middleware

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"logmara/model"

	"strings"

	"github.com/gin-gonic/gin"
)

type APIKeyPermissions struct {
	ExportJSON   bool `json:"export_json"`
	ExportParsed bool `json:"export_parsed"`
	ViewStats    bool `json:"view_stats"`
}

type ScopeFilters struct {
	Hostnames  []string `json:"hostnames"`
	Severities []string `json:"severities"`
}

func APIKeyAuth(database *sql.DB) gin.HandlerFunc {
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
		keyHash := hex.EncodeToString([]byte(apiKey))

		row := database.QueryRow(`
			SELECT id, key_hash, is_active, permissions, scope_filters, rate_limit_per_min, expires_at
			FROM api_keys
			WHERE key_hash = $1
		`, keyHash)

		var id int
		var storedHash string
		var isActive bool
		var permissionsJSON, scopeFiltersJSON []byte
		var rateLimitPerMin int
		var expiresAtNull bool

		err := row.Scan(&id, &storedHash, &isActive, &permissionsJSON, &scopeFiltersJSON, &rateLimitPerMin, &expiresAtNull)
		if err != nil {
			c.AbortWithError(http.StatusUnauthorized, model.NewUnauthorizedKey("unauthorized", "Invalid API key", nil))
			return
		}

		if !isActive {
			c.AbortWithError(http.StatusUnauthorized, model.NewUnauthorizedKey("unauthorized", "API key is inactive", nil))
			return
		}

		if expiresAtNull {
			c.AbortWithError(http.StatusUnauthorized, model.NewUnauthorizedKey("unauthorized", "API key has expired", nil))
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
		if !limiter.Allow() {
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
	rateLimitersMu sync.Mutex
	rateLimiters   = make(map[int]*RateLimiter)
)

type RateLimiter struct {
	tokens   int
	maxToken int
}

func NewRateLimiter(maxTokens int) *RateLimiter {
	rl := &RateLimiter{
		tokens:   maxTokens,
		maxToken: maxTokens,
	}
	go rl.refill()
	return rl
}

func (rl *RateLimiter) refill() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		rl.tokens = rl.maxToken
	}
}

func (rl *RateLimiter) Allow() bool {
	if rl.tokens > 0 {
		rl.tokens--
		return true
	}
	return false
}

func getRateLimiter(keyID int, rateLimit int) *RateLimiter {
	rateLimitersMu.Lock()
	defer rateLimitersMu.Unlock()
	if rl, ok := rateLimiters[keyID]; ok {
		return rl
	}
	rl := NewRateLimiter(rateLimit)
	rateLimiters[keyID] = rl
	return rl
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
	f := filters.(ScopeFilters)
	conds := []string{}
	if len(f.Hostnames) > 0 {
		placeholders := make([]string, len(f.Hostnames))
		vals := make([]any, len(f.Hostnames))
		for i, h := range f.Hostnames {
			placeholders[i] = "$" + string(rune(len(*args)+i+1))
			vals[i] = h
		}
		conds = append(conds, "host_address IN ("+strings.Join(placeholders, ",")+")")
		*args = append(*args, vals...)
	}
	if len(f.Severities) > 0 {
		placeholders := make([]string, len(f.Severities))
		vals := make([]any, len(f.Severities))
		for i, s := range f.Severities {
			placeholders[i] = "$" + string(rune(len(*args)+i+1))
			vals[i] = s
		}
		conds = append(conds, "severity IN ("+strings.Join(placeholders, ",")+")")
		*args = append(*args, vals...)
	}
	if len(conds) > 0 {
		if query != "" {
			query += " AND "
		}
		query += "(" + strings.Join(conds, " AND ") + ")"
	}
	return query, *args
}
