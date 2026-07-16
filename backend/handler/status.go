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
		val := db.GetSetting(database, "is_initialized", "false")
		c.JSON(http.StatusOK, gin.H{
			"initialized": val == "true",
		})
	}
}

func GetDbConfig() gin.HandlerFunc {
	return func(c *gin.Context) {
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			c.JSON(http.StatusOK, gin.H{
				"host":     "postgres",
				"port":     5432,
				"name":     "syslog_db",
				"user":     "syslog",
				"password": "",
			})
			return
		}

		u, err := url.Parse(dsn)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"host":     "",
				"port":     0,
				"name":     "",
				"user":     "",
				"password": "",
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
			"host":     host,
			"port":     port,
			"name":     name,
			"user":     user,
			"password": "",
		})
	}
}
