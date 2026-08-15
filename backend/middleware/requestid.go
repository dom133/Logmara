package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestID generates a unique request ID for each incoming request and
// attaches it to the context and response headers. This enables tracing
// individual requests across logs and services.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = uuid.New().String()
		}
		c.Set("X-Request-ID", reqID)
		c.Header("X-Request-ID", reqID)
		c.Next()
	}
}