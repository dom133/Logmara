package handler

import (
	"database/sql"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"syslog-gui/db"

	"github.com/gin-gonic/gin"
)

func CheckInitialized(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		starting := db.IsAppStarting()
		if starting {
			c.JSON(http.StatusOK, gin.H{
				"initialized": false,
				"starting":    true,
			})
			return
		}
		val := db.GetSetting(database, "is_initialized", "false")
		c.JSON(http.StatusOK, gin.H{
			"initialized": val == "true",
			"starting":    false,
		})
	}
}

// CheckInitializedStandalone reports status before a database connection
// exists (no DATABASE_URL at startup) - the wizard is always shown, never
// the "starting" spinner, since we're waiting on user input, not migration.
func CheckInitializedStandalone() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"initialized": false,
			"starting":    false,
		})
	}
}

func GetDbConfig() gin.HandlerFunc {
	return func(c *gin.Context) {
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			c.JSON(http.StatusOK, gin.H{
				"configured": false,
				"host":       "postgres",
				"port":       5432,
				"name":       "syslog_db",
				"user":       "syslog",
				"password":   "",
			})
			return
		}

		u, err := url.Parse(dsn)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"configured": true,
				"host":       "",
				"port":       0,
				"name":       "",
				"user":       "",
				"password":   "",
			})
			return
		}

		host := u.Hostname()
		portStr := u.Port()
		port := 5432
		if portStr != "" {
			port, _ = strconv.Atoi(portStr)
		}

		name := strings.TrimPrefix(u.Path, "/")
		user := u.User.Username()
		_, _ = u.User.Password()

		c.JSON(http.StatusOK, gin.H{
			"configured": true,
			"host":       host,
			"port":       port,
			"name":       name,
			"user":       user,
			"password":   "",
		})
	}
}
