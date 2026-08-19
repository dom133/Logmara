package handler

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"logmara/tailer"
	"logmara/util"
)

var (
	preUpdateRunning       atomic.Bool
	maintenanceTokenWarned atomic.Bool
)

// maintenanceTokenOK reports whether the request is accepted to trigger
// maintenance. When MAINTENANCE_TOKEN is configured, the X-Maintenance-Token
// header must match it (constant-time compare). When it is not configured the
// endpoint falls back to Docker-network isolation only (reachable solely from
// the Docker network) and a one-time warning nudges operators to set a token.
func maintenanceTokenOK(c *gin.Context) bool {
	expected := util.SecretFromEnv("MAINTENANCE_TOKEN")
	if expected == "" {
		if maintenanceTokenWarned.CompareAndSwap(false, true) {
			slog.Warn("maintenance: MAINTENANCE_TOKEN not set; /api/maintenance/pre-update is protected only by Docker-network isolation - set MAINTENANCE_TOKEN to require a bearer token")
		}
		return true
	}
	return subtle.ConstantTimeCompare([]byte(c.GetHeader("X-Maintenance-Token")), []byte(expected)) == 1
}

// MaintenancePreUpdate triggers the pre-update preparation sequence.
// It pauses ingestion and compacts logs, so it's gated by MAINTENANCE_TOKEN
// (when configured) on top of the Docker-network isolation.
func MaintenancePreUpdate(logFilePath string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !maintenanceTokenOK(c) {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "invalid or missing maintenance token",
			})
			return
		}
		if !preUpdateRunning.CompareAndSwap(false, true) {
			c.JSON(http.StatusConflict, gin.H{
				"error": "pre-update preparation already in progress",
			})
			return
		}
		defer preUpdateRunning.Store(false)

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if err := tailer.PrepareForUpdate(ctx, logFilePath); err != nil {
				slog.Error("maintenance: pre-update preparation failed", "error", err)
			}
		}()

		c.JSON(http.StatusAccepted, gin.H{
			"status":  "preparing",
			"message": "pre-update preparation started",
		})
	}
}

// MaintenanceStatus returns the current maintenance status.
// This endpoint is intentionally unauthenticated - it's only reachable
// from within the Docker network.
func MaintenanceStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": tailer.MaintenanceStatus(),
		})
	}
}
