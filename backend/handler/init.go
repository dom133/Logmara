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
		Host     string `json:"host" binding:"required"`
		Port     int    `json:"port" binding:"required"`
		Name     string `json:"name" binding:"required"`
		User     string `json:"user" binding:"required"`
		Password string `json:"password" binding:"required"`
	} `json:"database" binding:"required"`
	JWTSecret     string `json:"jwt_secret" binding:"required,min=16"`
	EncryptionKey string `json:"encryption_key" binding:"required,min=16"`
	CORSOrigins   string `json:"cors_origins"`
	LDAP          struct {
		Server       string `json:"server"`
		Port         int    `json:"port"`
		UseTLS       bool   `json:"use_tls"`
		VerifyCert   bool   `json:"verify_cert"`
		CaCert       string `json:"ca_cert"`
		BaseDN       string `json:"base_dn"`
		BindDN       string `json:"bind_dn"`
		BindPassword string `json:"bind_password"`
	} `json:"ldap"`
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
			"INSERT INTO users (username, password_hash, email, is_admin, role, is_active, auth_type) VALUES ($1, $2, $3, $4, $5, $6, 'local')",
			req.Admin.Username, hash, req.Admin.Email, true, "admin", true,
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
		if req.CORSOrigins != "" {
			settings["cors_origins"] = req.CORSOrigins
			descriptions["cors_origins"] = "Allowed CORS origins"
		}
		if req.LDAP.Server != "" {
			settings["ldap_server"] = req.LDAP.Server
			descriptions["ldap_server"] = "LDAP server address"
			settings["ldap_port"] = strconv.Itoa(req.LDAP.Port)
			descriptions["ldap_port"] = "LDAP port"
			settings["ldap_use_tls"] = strconv.FormatBool(req.LDAP.UseTLS)
			descriptions["ldap_use_tls"] = "Use TLS for LDAP"
			settings["ldap_verify_cert"] = strconv.FormatBool(req.LDAP.VerifyCert)
			descriptions["ldap_verify_cert"] = "Verify LDAP certificate"
			if req.LDAP.CaCert != "" {
				settings["ldap_ca_cert"] = req.LDAP.CaCert
				descriptions["ldap_ca_cert"] = "LDAP CA certificate"
			}
			if req.LDAP.BaseDN != "" {
				settings["ldap_base_dn"] = req.LDAP.BaseDN
				descriptions["ldap_base_dn"] = "LDAP base DN"
			}
			if req.LDAP.BindDN != "" {
				settings["ldap_bind_dn"] = req.LDAP.BindDN
				descriptions["ldap_bind_dn"] = "LDAP bind DN"
			}
			if req.LDAP.BindPassword != "" {
				settings["ldap_bind_password"] = req.LDAP.BindPassword
				descriptions["ldap_bind_password"] = "LDAP bind password"
			}
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