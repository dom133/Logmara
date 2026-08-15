package middleware

import (
	"os"

	"github.com/gin-gonic/gin"
)

// ServerIdentity stamps responses with an identifier for the api instance
// that served them, so a client (or admin poking around with curl) can tell
// which replica handled a given request. This only matters behind Docker
// Swarm's routing mesh (api:8080 VIP), where neither nginx nor HAProxy see
// which replica was picked - see the note in haproxy/haproxy-app.cfg.
//
// SWARM_TASK_IDENTITY (docker-stack.app.yml, "{{.Node.Hostname}}.
// {{.Task.Slot}}") gives a human-readable "node.replica" like "app1.2".
// Falls back to the container ID (os.Hostname()) when unset, e.g. plain
// docker-compose.yml, which has no Swarm task templating.
func ServerIdentity() gin.HandlerFunc {
	identity := os.Getenv("SWARM_TASK_IDENTITY")
	if identity == "" {
		var err error
		identity, err = os.Hostname()
		if err != nil {
			identity = "unknown"
		}
	}
	return func(c *gin.Context) {
		c.Header("X-Api-Server", identity)
		c.Next()
	}
}
