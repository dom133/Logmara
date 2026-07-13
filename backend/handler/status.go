package handler

import (
	"database/sql"
	"net/http"

	"syslog-gui/db"

	"github.com/gin-gonic/gin"
)

func CheckInitialized(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		val := db.GetSetting(database, "is_initialized", "false")
		c.JSON(http.StatusOK, gin.H{
			"initialized": val == "true",
		})
	}
}