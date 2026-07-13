package handler

import (
	"database/sql"
	"log"
	"net/http"
	"strconv"

	"syslog-gui/auth"
	"syslog-gui/db"
	"syslog-gui/util"

	"github.com/gin-gonic/gin"
)

type InitRequest struct {
	Admin struct {
		Username string `json:"username" binding:"required,min=3"`
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required,min=8"`
	} `json:"admin" binding:"required"`
	Database struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Name     string `json:"name"`
		User     string `json:"user"`
		Password string `json:"password"`
	} `json:"database"`
	JWTSecret      string `json:"jwt_secret" binding:"required,min=16"`
	EncryptionKey  string `json:"encryption_key" binding:"required,min=16"`
}

func Initialize(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		val := db.GetSetting(database, "is_initialized", "false")
		if val == "true" {
			c.JSON(http.StatusConflict, gin.H{"error": "Application already initialized"})
			return
		}

		var req InitRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
			return
		}

		tx, err := database.Begin()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not start transaction"})
			return
		}
		defer tx.Rollback()

		hash, err := auth.HashPassword(req.Admin.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not hash password"})
			return
		}

		_, err = tx.Exec(
			"INSERT INTO users (username, password_hash, is_admin, role, is_active) VALUES ($1, $2, $3, $4, $5)",
			req.Admin.Username, hash, true, "admin", true,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not create admin user"})
			return
		}

		settings := map[string]string{
			"is_initialized": "true",
			"jwt_secret":     req.JWTSecret,
			"encryption_key": req.EncryptionKey,
		}
		descriptions := map[string]string{
			"is_initialized": "Application initialization flag",
			"jwt_secret":     "JWT signing secret key",
			"encryption_key": "Encryption key for sensitive data",
		}

		if req.Database.Host != "" {
			settings["db_host"] = req.Database.Host
			descriptions["db_host"] = "Database host"
		}
		if req.Database.Port != 0 {
			settings["db_port"] = strconv.Itoa(req.Database.Port)
			descriptions["db_port"] = "Database port"
		}
		if req.Database.Name != "" {
			settings["db_name"] = req.Database.Name
			descriptions["db_name"] = "Database name"
		}
		if req.Database.User != "" {
			settings["db_user"] = req.Database.User
			descriptions["db_user"] = "Database user"
		}
		if req.Database.Password != "" {
			settings["db_password"] = req.Database.Password
			descriptions["db_password"] = "Database password"
		}

		insertSQL := `INSERT INTO app_settings (key, value, description) VALUES ($1, $2, $3)
			ON CONFLICT (key) DO UPDATE SET value = $2, description = $3`

		for k, v := range settings {
			if _, err := tx.Exec(insertSQL, k, v, descriptions[k]); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not save setting: " + k})
				return
			}
		}

		if err := tx.Commit(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not commit transaction"})
			return
		}

		log.Printf("Application initialized by admin user: %s", req.Admin.Username)
		c.JSON(http.StatusOK, gin.H{"message": "Application initialized successfully"})
	}
}

func GenerateKeys() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"jwt_secret":     util.GenerateJWTSecret(),
			"encryption_key": util.GenerateEncryptionKey(),
		})
	}
}