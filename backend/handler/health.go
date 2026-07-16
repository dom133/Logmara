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