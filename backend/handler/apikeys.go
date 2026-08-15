package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"logmara/audit"
	"logmara/middleware"
	"logmara/model"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

// validateAllowedIPs trims and checks each entry is a plain IP or CIDR
// range, mirroring the matching logic in middleware.ipAllowed - rejecting
// garbage here means a typo surfaces as a 400 at save time, not as a
// silently-never-matching allowlist entry later.
func validateAllowedIPs(ips []string) ([]string, error) {
	cleaned := make([]string, 0, len(ips))
	for _, raw := range ips {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return nil, fmt.Errorf("invalid CIDR range: %s", entry)
			}
		} else if net.ParseIP(entry) == nil {
			return nil, fmt.Errorf("invalid IP address: %s", entry)
		}
		cleaned = append(cleaned, entry)
	}
	return cleaned, nil
}

func GenerateAPIKey() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// hashAPIKey returns the SHA-256 digest of a plaintext API key, hex-encoded.
// Only the digest is ever persisted (key_hash) - the plaintext key is shown
// to the caller once, at creation/reset time, and never stored.
func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func CreateAPIKey(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, username := actorFromContext(c)

		var req struct {
			Name            string             `json:"name" binding:"required"`
			Permissions     map[string]bool    `json:"permissions"`
			ScopeFilters    *struct {
				Hostnames  []string `json:"hostnames"`
				Severities []string `json:"severities"`
				MatchMode  string   `json:"match_mode"`
			} `json:"scope_filters"`
			AllowedIPs      []string `json:"allowed_ips"`
			RateLimitPerMin int      `json:"rate_limit_per_min"`
			TTLDays         int      `json:"ttl_days"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.AbortWithError(http.StatusBadRequest, model.NewBadRequest("Invalid request body", err))
			return
		}

		if req.RateLimitPerMin <= 0 {
			req.RateLimitPerMin = 60
		}

		if req.ScopeFilters != nil && req.ScopeFilters.MatchMode != "or" {
			req.ScopeFilters.MatchMode = "and"
		}

		allowedIPs, err := validateAllowedIPs(req.AllowedIPs)
		if err != nil {
			c.AbortWithError(http.StatusBadRequest, model.NewBadRequest(err.Error(), err))
			return
		}

		permsJSON, _ := json.Marshal(req.Permissions)
		scopeJSON, _ := json.Marshal(req.ScopeFilters)

		var expiresAtPtr *time.Time
		if req.TTLDays > 0 {
			t := time.Now().AddDate(0, 0, req.TTLDays)
			expiresAtPtr = &t
		}

		key := GenerateAPIKey()
		keyHash := hashAPIKey(key)
		keyPrefix := key[:8]

		_, err = database.Exec(`
			INSERT INTO api_keys (name, key_hash, key_prefix, permissions, scope_filters, allowed_ips, rate_limit_per_min, expires_at, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, req.Name, keyHash, keyPrefix, permsJSON, scopeJSON, pq.Array(allowedIPs), req.RateLimitPerMin, expiresAtPtr, userID)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, model.NewInternal("Failed to create API key", err))
			return
		}

		audit.LogAudit(database, userID, username, "api_key_created", c.ClientIP(), fmt.Sprintf("name=%s, prefix=%s", req.Name, keyPrefix))

		c.JSON(http.StatusCreated, gin.H{
			"key":       key,
			"keyPrefix": keyPrefix,
		})
	}
}

func ListAPIKeys(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := database.Query(`
			SELECT k.id, k.name, k.key_prefix, k.permissions, k.scope_filters, k.allowed_ips, k.is_active,
			       k.rate_limit_per_min, k.expires_at, k.last_used_at, k.total_requests, k.created_at,
			       u.username AS created_by_username
			FROM api_keys k
			LEFT JOIN users u ON k.created_by = u.id
			ORDER BY k.created_at DESC
		`)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, model.NewInternal("Failed to list API keys", err))
			return
		}
		defer rows.Close()

		var keys []gin.H
		for rows.Next() {
			var id, rateLimit, totalReqs int
			var name, keyPrefix string
			var permsJSON, scopeJSON []byte
			var allowedIPs pq.StringArray
			var isActive bool
			var expiresAt, lastUsedAt sql.NullTime
			var createdAt time.Time
			var createdByUsername sql.NullString

			err := rows.Scan(&id, &name, &keyPrefix, &permsJSON, &scopeJSON, &allowedIPs, &isActive,
				&rateLimit, &expiresAt, &lastUsedAt, &totalReqs, &createdAt, &createdByUsername)
			if err != nil {
				continue
			}

			var perms map[string]bool
			json.Unmarshal(permsJSON, &perms)

			var scopeFilters map[string]interface{}
			if scopeJSON != nil {
				json.Unmarshal(scopeJSON, &scopeFilters)
			}

			var expiresAtOut, lastUsedAtOut interface{}
			if expiresAt.Valid {
				expiresAtOut = expiresAt.Time
			}
			if lastUsedAt.Valid {
				lastUsedAtOut = lastUsedAt.Time
			}

			keys = append(keys, gin.H{
				"id":                id,
				"name":              name,
				"keyPrefix":         keyPrefix,
				"permissions":       perms,
				"scope_filters":     scopeFilters,
				"allowed_ips":       []string(allowedIPs),
				"is_active":         isActive,
				"rate_limit_per_min": rateLimit,
				"expires_at":        expiresAtOut,
				"last_used_at":      lastUsedAtOut,
				"total_requests":    totalReqs,
				"created_at":        createdAt,
				"created_by":        createdByUsername.String,
			})
		}

		c.JSON(http.StatusOK, gin.H{"data": keys})
	}
}

func UpdateAPIKey(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, username := actorFromContext(c)
		idStr := c.Param("id")
		id, _ := strconv.Atoi(idStr)

		var req struct {
			Name          *string            `json:"name"`
			Permissions   *map[string]bool   `json:"permissions"`
			ScopeFilters  *struct {
				Hostnames  []string `json:"hostnames"`
				Severities []string `json:"severities"`
				MatchMode  string   `json:"match_mode"`
			} `json:"scope_filters"`
			AllowedIPs      *[]string `json:"allowed_ips"`
			IsActive        *bool     `json:"is_active"`
			RateLimitPerMin *int      `json:"rate_limit_per_min"`
			TTLDays         *int      `json:"ttl_days"`
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.AbortWithError(http.StatusBadRequest, model.NewBadRequest("Invalid request body", err))
			return
		}

		if req.ScopeFilters != nil && req.ScopeFilters.MatchMode != "or" {
			req.ScopeFilters.MatchMode = "and"
		}

		var cleanedAllowedIPs []string
		if req.AllowedIPs != nil {
			var err error
			cleanedAllowedIPs, err = validateAllowedIPs(*req.AllowedIPs)
			if err != nil {
				c.AbortWithError(http.StatusBadRequest, model.NewBadRequest(err.Error(), err))
				return
			}
		}

		setters := []string{}
		changedFields := []string{}
		args := []any{}
		argCount := 0

		if req.Name != nil {
			argCount++
			setters = append(setters, "name = $"+strconv.Itoa(argCount))
			changedFields = append(changedFields, "name")
			args = append(args, *req.Name)
		}

		if req.Permissions != nil {
			argCount++
			permsJSON, _ := json.Marshal(*req.Permissions)
			setters = append(setters, "permissions = $"+strconv.Itoa(argCount))
			changedFields = append(changedFields, "permissions")
			args = append(args, permsJSON)
		}

		if req.ScopeFilters != nil {
			argCount++
			scopeJSON, _ := json.Marshal(*req.ScopeFilters)
			setters = append(setters, "scope_filters = $"+strconv.Itoa(argCount))
			changedFields = append(changedFields, "scope_filters")
			args = append(args, scopeJSON)
		}

		if req.AllowedIPs != nil {
			argCount++
			setters = append(setters, "allowed_ips = $"+strconv.Itoa(argCount))
			changedFields = append(changedFields, "allowed_ips")
			args = append(args, pq.Array(cleanedAllowedIPs))
		}

		if req.IsActive != nil {
			argCount++
			setters = append(setters, "is_active = $"+strconv.Itoa(argCount))
			changedFields = append(changedFields, "is_active")
			args = append(args, *req.IsActive)
		}

		if req.RateLimitPerMin != nil {
			argCount++
			setters = append(setters, "rate_limit_per_min = $"+strconv.Itoa(argCount))
			changedFields = append(changedFields, "rate_limit_per_min")
			args = append(args, *req.RateLimitPerMin)
		}

		if req.TTLDays != nil {
			argCount++
			var expiresAt *time.Time
			if *req.TTLDays > 0 {
				t := time.Now().AddDate(0, 0, *req.TTLDays)
				expiresAt = &t
			}
			setters = append(setters, "expires_at = $"+strconv.Itoa(argCount))
			changedFields = append(changedFields, "expires_at")
			args = append(args, expiresAt)
		}

		if len(setters) == 0 {
			c.AbortWithError(http.StatusBadRequest, model.NewBadRequest("No fields to update", nil))
			return
		}

		argCount++
		args = append(args, id)

		query := "UPDATE api_keys SET " + joinSQL(setters) + " WHERE id = $" + strconv.Itoa(argCount)
		_, err := database.Exec(query, args...)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, model.NewInternal("Failed to update API key", err))
			return
		}

		audit.LogAudit(database, userID, username, "api_key_updated", c.ClientIP(), fmt.Sprintf("id=%d, fields=%s", id, strings.Join(changedFields, ",")))

		c.JSON(http.StatusOK, gin.H{"message": "API key updated"})
	}
}

func DeleteAPIKey(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, username := actorFromContext(c)
		idStr := c.Param("id")
		id, _ := strconv.Atoi(idStr)

		_, err := database.Exec("DELETE FROM api_keys WHERE id = $1", id)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, model.NewInternal("Failed to delete API key", err))
			return
		}

		audit.LogAudit(database, userID, username, "api_key_deleted", c.ClientIP(), fmt.Sprintf("id=%d", id))
		middleware.RemoveRateLimiter(id)

		c.JSON(http.StatusOK, gin.H{"message": "API key deleted"})
	}
}

func ResetAPIKey(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, username := actorFromContext(c)
		idStr := c.Param("id")
		id, _ := strconv.Atoi(idStr)

		key := GenerateAPIKey()
		keyHash := hashAPIKey(key)
		keyPrefix := key[:8]

		_, err := database.Exec("UPDATE api_keys SET key_hash = $1, key_prefix = $2 WHERE id = $3", keyHash, keyPrefix, id)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, model.NewInternal("Failed to reset API key", err))
			return
		}

		audit.LogAudit(database, userID, username, "api_key_reset", c.ClientIP(), fmt.Sprintf("id=%d, prefix=%s", id, keyPrefix))

		c.JSON(http.StatusOK, gin.H{
			"key":       key,
			"keyPrefix": keyPrefix,
		})
	}
}

func joinSQL(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	return result
}
