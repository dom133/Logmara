package handler

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

func HealthCheck(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := database.Ping(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status":  "unhealthy",
				"db":      "disconnected",
				"message": "database unreachable",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "healthy",
			"db":     "connected",
		})
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