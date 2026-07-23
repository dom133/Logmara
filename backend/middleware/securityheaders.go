package middleware

import (
	"github.com/gin-gonic/gin"
)

func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
		c.Writer.Header().Set("X-Frame-Options", "DENY")
		c.Writer.Header().Set("X-XSS-Protection", "1; mode=block")
		c.Writer.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Writer.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		c.Writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Writer.Header().Del("Server")
		// nginx sets X-Forwarded-Proto to the scheme the client actually
		// used ($scheme), in both the :80 and :443 server blocks - only tell
		// the browser to force HTTPS on this host going forward if this
		// particular request already arrived over it, so plain-HTTP-only
		// deployments (https_enabled off) aren't affected.
		if c.GetHeader("X-Forwarded-Proto") == "https" {
			c.Writer.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
