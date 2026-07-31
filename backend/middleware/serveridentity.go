package middleware

import (
	"os"

	"github.com/gin-gonic/gin"
)

// ServerIdentity stamps responses with the container's hostname so a client
// (or admin poking around with curl) can tell which api replica served a
// given request. This only matters behind Docker Swarm's routing mesh
// (api:8080 VIP), where neither nginx nor HAProxy see which replica was
// picked - see the note in haproxy/haproxy-app.cfg.
func ServerIdentity() gin.HandlerFunc {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	return func(c *gin.Context) {
		c.Header("X-Api-Server", hostname)
		c.Next()
	}
}
