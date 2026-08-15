package middleware

import (
	"log/slog"

	"logmara/db"

	"github.com/gin-gonic/gin"
)

// UpdateSessionActivity marks the current session as active by updating
// last_used_at on the refresh_tokens row matching the JWT's jti.
// Runs asynchronously so it does not block the response.
func UpdateSessionActivity(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		jti, ok := c.Get("jti")
		if !ok {
			c.Next()
			return
		}
		jtiStr, ok := jti.(string)
		if !ok || jtiStr == "" {
			c.Next()
			return
		}
		go func() {
			_, err := pool.Get().Exec(
				"UPDATE refresh_tokens SET last_used_at = NOW() WHERE jti = $1 AND used = false",
				jtiStr,
			)
			if err != nil {
				slog.Error("failed to update session activity", "err", err)
			}
		}()
		c.Next()
	}
}
