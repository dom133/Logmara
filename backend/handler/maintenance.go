package handler

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"logmara/tailer"
)

var preUpdateRunning atomic.Bool

// MaintenancePreUpdate triggers the pre-update preparation sequence.
// This endpoint is intentionally unauthenticated - it's only reachable
// from within the Docker network (not exposed publicly).
func MaintenancePreUpdate(logFilePath string) gin.HandlerFunc {
	return func(c *gin.Context) {
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
