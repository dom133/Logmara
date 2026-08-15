package handler

import (
	"database/sql"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
)

var startTime = time.Now()

func HealthCheck(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		dbOK := true
		dbMsg := "connected"

		if err := database.Ping(); err != nil {
			dbOK = false
			dbMsg = "disconnected"
		}

		status := "healthy"
		if !dbOK {
			status = "unhealthy"
		}

		stats := database.Stats()
		up := time.Since(startTime).Seconds()

		c.JSON(http.StatusOK, gin.H{
			"status":            status,
			"db":                dbMsg,
			"uptime_seconds":    up,
			"goroutines":        runtime.NumGoroutine(),
			"db_open_conns":     stats.OpenConnections,
			"db_in_use_conns":   stats.InUse,
			"db_idle_conns":     stats.Idle,
			"db_max_open_conns": stats.MaxOpenConnections,
		})

		if !dbOK {
			c.Status(http.StatusServiceUnavailable)
		}
	}
}

// HealthCheckStandalone reports the process as up while it waits for the
// setup wizard to supply database settings (no DATABASE_URL at startup).
func HealthCheckStandalone() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "awaiting_setup",
			"db":     "not_configured",
		})
	}
}
