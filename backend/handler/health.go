package handler

import (
	"database/sql"
	"net/http"
	"runtime"
	"time"

	"logmara/db"

	"github.com/gin-gonic/gin"
)

var startTime = time.Now()

// HealthCheck backs the Dockerfile's HEALTHCHECK (CMD curl .../api/health),
// which in turn gates every "depends_on: api: condition: service_healthy"
// dependent (e.g. rsyslog in docker-compose.yml). It reports "starting"
// (still HTTP 200, so curl -f and Docker's health status both succeed)
// while db.IsAppStarting() is true, rather than failing the check - the
// slower part of startup (RefreshMaterializedViews/ApplyEnvSettingOverrides,
// see main.go) can take as long as the accumulated log volume does, and
// this must not turn into an "unhealthy" container / block dependents on
// every restart just because that background pass hasn't finished yet.
func HealthCheck(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if db.IsAppStarting() {
			c.JSON(http.StatusOK, gin.H{
				"status": "starting",
				"db":     "initializing",
			})
			return
		}

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
