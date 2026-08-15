package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"

	"syslog-gui/auth"
	"syslog-gui/db"
	"syslog-gui/middleware"
	"syslog-gui/model"
	"syslog-gui/util"

	"github.com/gin-gonic/gin"
)

type InitRequest struct {
	Admin struct {
		Username string `json:"username" binding:"required,min=3,max=100"`
		Email    string `json:"email" binding:"required,email,max=256"`
		Password string `json:"password" binding:"required,min=8,max=128"`
	} `json:"admin" binding:"required"`
	Database struct {
		Host     string `json:"host" binding:"required,max=256"`
		Port     int    `json:"port" binding:"required"`
		Name     string `json:"name" binding:"required,max=128"`
		User     string `json:"user" binding:"required,max=128"`
		Password string `json:"password" binding:"required,max=256"`
	} `json:"database" binding:"required"`
	JWTSecret     string `json:"jwt_secret" binding:"required,min=16,max=512"`
	EncryptionKey string `json:"encryption_key" binding:"required,min=16,max=512"`
	CORSOrigins   string `json:"cors_origins" binding:"max=1024"`
	LDAP          struct {
		Server       string `json:"server" binding:"max=256"`
		Port         int    `json:"port"`
		UseTLS       bool   `json:"use_tls"`
		VerifyCert   bool   `json:"verify_cert"`
		CaCert       string `json:"ca_cert" binding:"max=8192"`
		BaseDN       string `json:"base_dn" binding:"max=512"`
		BindDN       string `json:"bind_dn" binding:"max=512"`
		BindPassword string `json:"bind_password" binding:"max=256"`
	} `json:"ldap"`
}

func Initialize(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		val := db.GetSetting(database, "is_initialized", "false")
		if val == "true" {
			middleware.HandleError(c, model.NewConflict("Application already initialized", nil))
			return
		}

		var req InitRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Invalid request", err))
			return
		}
		if err := auth.ValidatePassword(req.Admin.Password); err != nil {
			middleware.HandleError(c, model.NewBadRequest(err.Error(), nil))
			return
		}

		tx, err := database.Begin()
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Could not start transaction", err))
			return
		}
		defer tx.Rollback()

		hash, err := auth.HashPassword(req.Admin.Password)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Could not hash password", err))
			return
		}

		_, err = tx.Exec(
			"INSERT INTO users (username, password_hash, email, is_admin, role, is_active, auth_type) VALUES ($1, $2, $3, $4, $5, $6, 'local')",
			req.Admin.Username, hash, req.Admin.Email, true, RoleAdmin, true,
		)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Could not create admin user", err))
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
				middleware.HandleError(c, model.NewInternal("Could not save setting: "+k, err))
				return
			}
		}

		if err := tx.Commit(); err != nil {
			middleware.HandleError(c, model.NewInternal("Could not commit transaction", err))
			return
		}

		slog.Info("application initialized", "admin", req.Admin.Username)
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
