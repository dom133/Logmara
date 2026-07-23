package handler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"syslytics/auth"
	"syslytics/db"
	"syslytics/middleware"
	"syslytics/model"
	"syslytics/util"

	"github.com/gin-gonic/gin"
)

type DatabaseConfig struct {
	Host     string `json:"host" binding:"max=256"`
	Port     int    `json:"port"`
	Name     string `json:"name" binding:"max=128"`
	User     string `json:"user" binding:"max=128"`
	Password string `json:"password" binding:"max=256"`
}

type InitRequest struct {
	Admin struct {
		Username string `json:"username" binding:"required,min=3,max=100"`
		Email    string `json:"email" binding:"required,email,max=256"`
		Password string `json:"password" binding:"required,min=8,max=128"`
	} `json:"admin" binding:"required"`
	// Database is only required when the server has no DATABASE_URL of its
	// own (see InitializeStandalone) - when a connection is already
	// established via env, this is ignored.
	Database      DatabaseConfig `json:"database"`
	JWTSecret     string         `json:"jwt_secret" binding:"required,min=16,max=512"`
	EncryptionKey string         `json:"encryption_key" binding:"required,min=16,max=512"`
	CORSOrigins   string         `json:"cors_origins" binding:"max=1024"`
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

// bindInitRequest parses and validates the common parts of an init request,
// writing an error response itself and returning ok=false on failure.
func bindInitRequest(c *gin.Context) (*InitRequest, bool) {
	var req InitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.HandleError(c, model.NewBadRequest("Invalid request", err))
		return nil, false
	}
	if err := auth.ValidatePassword(req.Admin.Password); err != nil {
		middleware.HandleError(c, model.NewBadRequest("Password does not meet requirements", err))
		return nil, false
	}
	return &req, true
}

// createAdminAndSettings creates the initial admin user and persists the
// submitted settings. It's shared by Initialize (DB already connected via
// env) and InitializeStandalone (DB connected from wizard-submitted config).
func createAdminAndSettings(database *sql.DB, req *InitRequest) *model.AppError {
	val := db.GetSetting(database, "is_initialized", "false")
	if val == "true" {
		return model.NewConflict("Application already initialized", nil)
	}

	tx, err := database.Begin()
	if err != nil {
		return model.NewInternal("Could not start transaction", err)
	}
	defer tx.Rollback()

	hash, err := auth.HashPassword(req.Admin.Password)
	if err != nil {
		return model.NewInternal("Could not hash password", err)
	}

	_, err = tx.Exec(
		"INSERT INTO users (username, password_hash, email, is_admin, role, is_active, auth_type) VALUES ($1, $2, $3, $4, $5, $6, 'local')",
		req.Admin.Username, hash, req.Admin.Email, true, RoleAdmin, true,
	)
	if err != nil {
		return model.NewInternal("Could not create admin user", err)
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

	insertSQL := `INSERT INTO app_settings (key, value, description) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET value = $2, description = $3`

	for k, v := range settings {
		if _, err := tx.Exec(insertSQL, k, v, descriptions[k]); err != nil {
			return model.NewInternal("Could not save setting: "+k, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return model.NewInternal("Could not commit transaction", err)
	}

	slog.Info("application initialized", "admin", req.Admin.Username)
	return nil
}

// Initialize handles setup-wizard submission when a database connection is
// already established (DATABASE_URL was set at startup). Any submitted
// Database fields are ignored - the live connection is authoritative.
func Initialize(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := bindInitRequest(c)
		if !ok {
			return
		}

		if appErr := createAdminAndSettings(database, req); appErr != nil {
			middleware.HandleError(c, appErr)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Application initialized successfully"})
	}
}

// buildDSN turns wizard-submitted database settings into a postgres DSN.
func buildDSN(d DatabaseConfig) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(d.User, d.Password),
		Host:     fmt.Sprintf("%s:%d", d.Host, d.Port),
		Path:     "/" + d.Name,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

// TestDatabaseConfig checks that the submitted database settings can
// actually be connected to, without migrating or creating anything. Used by
// the setup wizard's "Test Connection" button before letting the user move
// past the Database step.
func TestDatabaseConfig() gin.HandlerFunc {
	return func(c *gin.Context) {
		var d DatabaseConfig
		if err := c.ShouldBindJSON(&d); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Invalid request", err))
			return
		}
		if d.Host == "" || d.Port == 0 || d.Name == "" || d.User == "" || d.Password == "" {
			middleware.HandleError(c, model.NewBadRequest("Database host, port, name, user and password are all required", nil))
			return
		}

		conn, err := sql.Open("postgres", buildDSN(d))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("Invalid database settings", err))
			return
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		if err := conn.PingContext(ctx); err != nil {
			middleware.HandleError(c, model.NewServiceUnavailable("Could not connect to the database with the provided settings", err))
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Connection successful"})
	}
}

// InitializeStandalone handles setup-wizard submission when the server has
// no database connection yet (no DATABASE_URL was set at startup). It
// connects using the submitted database settings, migrates the schema, and
// creates the admin account, then hands the live connection to main() over
// ready so the full application can come up on it.
func InitializeStandalone(ready chan<- *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := bindInitRequest(c)
		if !ok {
			return
		}

		d := req.Database
		if d.Host == "" || d.Port == 0 || d.Name == "" || d.User == "" || d.Password == "" {
			middleware.HandleError(c, model.NewBadRequest("Database host, port, name, user and password are all required", nil))
			return
		}

		database, err := db.Connect(buildDSN(d))
		if err != nil {
			middleware.HandleError(c, model.NewServiceUnavailable("Could not connect to the database with the provided settings", err))
			return
		}

		if err := db.Migrate(database); err != nil {
			database.Close()
			middleware.HandleError(c, model.NewInternal("Database migration failed", err))
			return
		}

		if appErr := createAdminAndSettings(database, req); appErr != nil {
			database.Close()
			middleware.HandleError(c, appErr)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Application initialized successfully"})
		ready <- database
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
