package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

var ErrMaxRequestBodySize = errors.New("request body too large")

func RequireJSON() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodPost || c.Request.Method == http.MethodPut || c.Request.Method == http.MethodPatch {
			ct := c.GetHeader("Content-Type")
			if ct != "application/json" {
				c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{"error": "Content-Type must be application/json"})
				return
			}
		}
		c.Next()
	}
}

func MaxRequestBodySize(maxSize int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxSize {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Request body too large"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSize)
		c.Next()
	}
}
