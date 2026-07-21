package handler

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"syslytics/audit"
	"syslytics/auth"
	"syslytics/control"
	"syslytics/db"
	"syslytics/ldap"

	"github.com/gin-gonic/gin"
)

type CreateUserRequest struct {
	Username string `json:"username" binding:"required,max=100"`
	Email    string `json:"email" binding:"required,email,max=256"`
	Password string `json:"password" binding:"max=128"`
	Role     string `json:"role" binding:"required"`
	AuthType string `json:"auth_type"`
}

type UpdateUserRequest struct {
	Role     *string `json:"role"`
	IsActive *bool   `json:"is_active"`
}

type ResetPasswordRequest struct {
	Password string `json:"password" binding:"required,min=8,max=128"`
}

func ListUsers(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		users, err := db.GetAllUsers(database)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, users)
	}
}

func CreateUser(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		validRoles := []string{RoleAdmin, RoleEditor, RoleViewer}
		found := false
		for _, r := range validRoles {
			if req.Role == r {
				found = true
				break
			}
		}
		if !found {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role. Must be admin, editor, or viewer"})
			return
		}

		authType := req.AuthType
		if authType == "" {
			authType = "local"
		}

		if authType == "ldap" {
			isAdmin := req.Role == RoleAdmin
			user, err := db.CreateLDAPUser(database, req.Username, req.Email, req.Role, isAdmin)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			actorID, actorName := actorFromContext(c)
			audit.LogAudit(database, actorID, actorName, "user_created", c.ClientIP(), fmt.Sprintf("created %s user %s", authType, req.Username))
			c.JSON(http.StatusCreated, user)
			return
		}

		if req.Password == "" || len(req.Password) < 8 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Password is required and must be at least 8 characters"})
			return
		}
		if err := auth.ValidatePassword(req.Password); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not hash password"})
			return
		}

		isAdmin := req.Role == RoleAdmin
		user, err := db.CreateUser(database, req.Username, hash, req.Email, isAdmin, req.Role)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "user_created", c.ClientIP(), fmt.Sprintf("created local user %s", req.Username))
		c.JSON(http.StatusCreated, user)
	}
}

func UpdateUser(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		var req UpdateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if req.Role != nil {
			validRoles := []string{RoleAdmin, RoleEditor, RoleViewer}
			found := false
			for _, r := range validRoles {
				if *req.Role == r {
					found = true
					break
				}
			}
			if !found {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role"})
				return
			}
		}

		user, err := db.UpdateUser(database, id, req.Role, req.IsActive)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "user_updated", c.ClientIP(), fmt.Sprintf("updated user %s", user.Username))
		c.JSON(http.StatusOK, user)
	}
}

func DeleteUser(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		if err := db.DeleteUser(database, id); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "user_deleted", c.ClientIP(), fmt.Sprintf("deleted user id %d", id))
		c.JSON(http.StatusOK, gin.H{"message": "User deleted"})
	}
}

func ResetPassword(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		var req ResetPasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := auth.ValidatePassword(req.Password); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		var authType string
		if err := database.QueryRow("SELECT auth_type FROM users WHERE id = $1", id).Scan(&authType); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "User not found"})
			return
		}
		if authType == "ldap" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot reset password for LDAP users"})
			return
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not hash password"})
			return
		}

		if err := db.ResetUserPassword(database, id, hash); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "password_reset_by_admin", c.ClientIP(), fmt.Sprintf("reset password for user id %d", id))
		c.JSON(http.StatusOK, gin.H{"message": "Password reset successful"})
	}
}

func GetSettings(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		settings, err := db.GetAllSettings(database)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Not used by the frontend and not worth exposing: encryption keys and
		// the one-time DB connection settings captured during setup.
		for _, k := range []string{"jwt_secret", "encryption_key", "db_host", "db_port", "db_name", "db_user", "db_password", "vapid_public_key", "vapid_private_key", "https_enabled_env_applied", "https_redirect_env_applied"} {
			delete(settings, k)
		}
		if v, ok := settings["ldap_bind_password"]; ok && v != "" {
			settings["ldap_bind_password"] = "****"
		}
		if v, ok := settings["smtp_password"]; ok && v != "" {
			settings["smtp_password"] = "****"
		}
		c.JSON(http.StatusOK, settings)
	}
}

func UpdateSettings(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var settings map[string]string
		if err := c.ShouldBindJSON(&settings); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		oldHttpsEnabled := db.GetSetting(database, "https_enabled", "false")
		oldHttpsRedirect := db.GetSetting(database, "https_redirect", "false")
		oldCorsOrigins := db.GetSetting(database, "cors_origins", "")
		oldRelayEnabled := db.GetSetting(database, "relay_ingestion_enabled", "false")

		// Callers (e.g. the Syslog Relay page) may submit a partial settings
		// map containing only the key(s) they actually changed - default each
		// of these to its current value rather than "" when absent, so an
		// unrelated partial update can't be misread as "turn this off" below.
		newHttpsEnabled := settings["https_enabled"]
		if _, ok := settings["https_enabled"]; !ok {
			newHttpsEnabled = oldHttpsEnabled
		}
		newHttpsRedirect := settings["https_redirect"]
		if _, ok := settings["https_redirect"]; !ok {
			newHttpsRedirect = oldHttpsRedirect
		}
		newCorsOrigins := settings["cors_origins"]
		if _, ok := settings["cors_origins"]; !ok {
			newCorsOrigins = oldCorsOrigins
		}
		newRelayEnabled := settings["relay_ingestion_enabled"]
		if _, ok := settings["relay_ingestion_enabled"]; !ok {
			newRelayEnabled = oldRelayEnabled
		}

		if newHttpsEnabled == "true" && oldHttpsEnabled != "true" {
			sslDir := os.Getenv("SSL_DIR")
			if sslDir == "" {
				sslDir = "/data/ssl"
			}
			certPath := filepath.Join(sslDir, "server.crt")
			keyPath := filepath.Join(sslDir, "server.key")
			if _, err := os.Stat(certPath); os.IsNotExist(err) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot enable HTTPS: SSL certificate not found. Please upload certificate and key first."})
				return
			}
			if _, err := os.Stat(keyPath); os.IsNotExist(err) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot enable HTTPS: SSL private key not found. Please upload certificate and key first."})
				return
			}
		}

		for k, v := range settings {
			if err := db.UpdateSetting(database, k, v); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to update setting: " + k})
				return
			}
		}

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "settings_updated", c.ClientIP(), "")

		nginxConfigChanged := oldHttpsEnabled != newHttpsEnabled || oldHttpsRedirect != newHttpsRedirect || oldCorsOrigins != newCorsOrigins

		if nginxConfigChanged {
			// A handful of quick retries absorbs the case where the admin
			// toggles this shortly after `docker compose up`, before the
			// frontend's reload sidecar has come up - without making the
			// save button hang for the same ~30s startup budget used
			// elsewhere.
			if err := reloadNginxWithRetry(newHttpsEnabled == "true", newHttpsRedirect == "true", newCorsOrigins, 5, 2*time.Second); err != nil {
				slog.Warn("nginx reload failed after settings update", "error", err)
				c.JSON(http.StatusOK, gin.H{
					"message":            "Settings updated",
					"nginx_reload_error": err.Error(),
				})
				return
			}
		}

		if newRelayEnabled != "" && newRelayEnabled != oldRelayEnabled {
			if err := SyncRelayConfig(database); err != nil {
				slog.Warn("relay config sync failed after settings update", "error", err)
				c.JSON(http.StatusOK, gin.H{
					"message":            "Settings updated",
					"relay_reload_error": err.Error(),
				})
				return
			}
		}

		c.JSON(http.StatusOK, gin.H{"message": "Settings updated"})
	}
}

// httpsServerBlock is the nginx 443 server block, written verbatim to
// https.conf whenever https_enabled is on. It mirrors the :80 server block
// in frontend/nginx.conf.
const httpsServerBlock = `server {
    listen 443 ssl;
    server_name localhost;

    ssl_certificate /data/ssl/server.crt;
    ssl_certificate_key /data/ssl/server.key;

    root /usr/share/nginx/html;
    index index.html;

    location / {
        try_files $uri $uri/ /index.html;
    }

    location /api/notifications/stream {
        add_header Access-Control-Allow-Origin $cors_allow_origin always;
        add_header Access-Control-Allow-Methods "GET, POST, PUT, DELETE, PATCH, OPTIONS" always;
        add_header Access-Control-Allow-Headers "Content-Type, Authorization" always;
        proxy_pass http://api:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
        proxy_http_version 1.1;
    }

    location /api/ {
        add_header Access-Control-Allow-Origin $cors_allow_origin always;
        add_header Access-Control-Allow-Methods "GET, POST, PUT, DELETE, PATCH, OPTIONS" always;
        add_header Access-Control-Allow-Headers "Content-Type, Authorization" always;
        if ($request_method = OPTIONS) {
            return 204;
        }
        proxy_pass http://api:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
`

// corsMapDirective renders the nginx `map` block that resolves the
// request's Origin header to an Access-Control-Allow-Origin value: "*"
// allows any origin, an empty list allows none. This is the only CORS
// enforcement in the app - clients only ever reach the API through this
// nginx proxy, so there's nothing equivalent on the backend side.
func corsMapDirective(origins string) string {
	var b strings.Builder
	b.WriteString("map $http_origin $cors_allow_origin {\n")
	b.WriteString("    default \"\";\n")
	for _, o := range strings.Split(origins, ",") {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" {
			return "map $http_origin $cors_allow_origin {\n    default $http_origin;\n}\n"
		}
		b.WriteString(fmt.Sprintf("    %q $http_origin;\n", o))
	}
	b.WriteString("}\n")
	return b.String()
}

// reloadNginx writes the https.conf (443 server block, present only when
// httpsEnabled), redirect.conf (HTTP->HTTPS redirect, only meaningful when
// https is actually enabled), and cors.conf (Origin match map used by both
// server blocks) fragments consumed by nginx, then asks the frontend
// container's reload sidecar to apply them.
func reloadNginx(httpsEnabled, redirectEnabled bool, corsOrigins string) error {
	confDir := os.Getenv("NGINX_CONF_DIR")
	if confDir == "" {
		confDir = "/data/nginx"
	}
	if err := os.MkdirAll(confDir, 0755); err != nil {
		return fmt.Errorf("create nginx conf dir: %w", err)
	}

	httpsConf := ""
	if httpsEnabled {
		httpsConf = httpsServerBlock
	}
	if err := os.WriteFile(filepath.Join(confDir, "https.conf"), []byte(httpsConf), 0644); err != nil {
		return fmt.Errorf("write https.conf: %w", err)
	}

	redirectConf := ""
	if httpsEnabled && redirectEnabled {
		redirectConf = "return 301 https://$host$request_uri;\n"
	}
	if err := os.WriteFile(filepath.Join(confDir, "redirect.conf"), []byte(redirectConf), 0644); err != nil {
		return fmt.Errorf("write redirect.conf: %w", err)
	}

	if err := os.WriteFile(filepath.Join(confDir, "cors.conf"), []byte(corsMapDirective(corsOrigins)), 0644); err != nil {
		return fmt.Errorf("write cors.conf: %w", err)
	}

	return postReloadRequests()
}

// postReloadRequests hits the frontend's reload sidecar. With a single
// frontend replica (the default - NGINX_RELOAD_TARGETS_HOST unset), this is
// exactly the original single-target POST via NGINX_RELOAD_URL. With
// multiple frontend replicas behind Swarm, NGINX_RELOAD_TARGETS_HOST is set
// to a DNS name that resolves to every replica's task IP (e.g. Swarm's
// "tasks.frontend", which - unlike the plain "frontend" service name -
// always returns one A record per running task rather than round-robining
// through a single VIP) and every one of them gets reloaded in parallel.
func postReloadRequests() error {
	targetsHost := os.Getenv("NGINX_RELOAD_TARGETS_HOST")
	if targetsHost == "" {
		reloadURL := os.Getenv("NGINX_RELOAD_URL")
		if reloadURL == "" {
			reloadURL = "http://frontend:8081/cgi-bin/reload.sh"
		}
		return postReload(reloadURL)
	}

	port := os.Getenv("NGINX_RELOAD_PORT")
	if port == "" {
		port = "8081"
	}

	ips, err := net.LookupHost(targetsHost)
	if err != nil {
		return fmt.Errorf("resolve nginx reload targets %q: %w", targetsHost, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("nginx reload targets %q resolved to no addresses", targetsHost)
	}

	type reloadResult struct {
		ip  string
		err error
	}
	results := make(chan reloadResult, len(ips))
	for _, ip := range ips {
		go func(ip string) {
			url := fmt.Sprintf("http://%s:%s/cgi-bin/reload.sh", ip, port)
			results <- reloadResult{ip: ip, err: postReload(url)}
		}(ip)
	}

	succeeded := 0
	var failures []string
	for i := 0; i < len(ips); i++ {
		r := <-results
		if r.err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", r.ip, r.err))
			continue
		}
		succeeded++
	}

	if succeeded == 0 {
		return fmt.Errorf("nginx reload failed on all %d target(s): %s", len(ips), strings.Join(failures, "; "))
	}
	if len(failures) > 0 {
		// Partial failure is expected during a rolling deploy (a replica
		// briefly not listening while it restarts) - log it but don't fail
		// the whole settings update over it.
		slog.Warn("nginx reload failed on some targets", "succeeded", succeeded, "total", len(ips), "errors", strings.Join(failures, "; "))
	}
	return nil
}

func postReload(url string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "text/plain", nil)
	if err != nil {
		return fmt.Errorf("nginx reload request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("nginx reload returned status %d", resp.StatusCode)
	}
	return nil
}

// reloadNginxWithRetry retries reloadNginx a few times with a fixed delay,
// smoothing over the brief window (container startup, or an admin action
// that races it) where the frontend's reload sidecar isn't listening yet.
func reloadNginxWithRetry(httpsEnabled, redirectEnabled bool, corsOrigins string, attempts int, delay time.Duration) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = reloadNginx(httpsEnabled, redirectEnabled, corsOrigins); err == nil {
			return nil
		}
		if i < attempts-1 {
			time.Sleep(delay)
		}
	}
	return err
}

// ReloadNginx re-applies the current HTTPS/redirect/CORS settings and
// triggers an nginx config reload via the frontend container's sidecar.
func ReloadNginx(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := SyncNginxHTTPS(database); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "nginx reloaded"})
	}
}

// SyncNginxHTTPS applies the persisted https_enabled/https_redirect/
// cors_origins settings to the frontend's nginx config, with the same short
// retry budget as UpdateSettings. Call this at backend startup (after
// migration/env overrides) so a container restart converges nginx to the
// stored setting instead of leaving whatever was baked into the image or
// left over from a previous state.
func SyncNginxHTTPS(database *sql.DB) error {
	return SyncNginxHTTPSWithRetry(database, 5, 2*time.Second)
}

// SyncNginxHTTPSWithRetry is SyncNginxHTTPS with a caller-chosen retry
// budget - used at startup with a much larger budget than the interactive
// endpoints, since a cold `docker compose up` can leave the frontend
// container down for a while and there's no request to keep fast there.
func SyncNginxHTTPSWithRetry(database *sql.DB, attempts int, delay time.Duration) error {
	httpsEnabled := db.GetSetting(database, "https_enabled", "false") == "true"
	redirectEnabled := db.GetSetting(database, "https_redirect", "false") == "true"
	corsOrigins := db.GetSetting(database, "cors_origins", "")
	return reloadNginxWithRetry(httpsEnabled, redirectEnabled, corsOrigins, attempts, delay)
}

func CleanupLogs(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		retentionDays := 30
		if val := db.GetSetting(database, "retention_days", "30"); val != "" {
			if d, err := strconv.Atoi(val); err == nil {
				retentionDays = d
			}
		}

		deleted, err := db.CleanupOldLogs(database, retentionDays)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":       "Cleanup completed",
			"deleted_count": deleted,
		})
	}
}

type PurgeRequest struct {
	PauseDuringPurge bool `json:"pause_during_purge"`
}

func PurgeAllLogs(database *sql.DB, ic control.IngestionController) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req PurgeRequest
		_ = c.ShouldBindJSON(&req)

		wasPaused := ic.IsPaused()
		if req.PauseDuringPurge {
			ic.Pause()
			time.Sleep(500 * time.Millisecond)
		}

		row := database.QueryRow("SELECT COUNT(*) FROM syslog_logs")
		var count int
		row.Scan(&count)
		slog.Info("purge started", "count", count)

		_, err := database.Exec("TRUNCATE TABLE syslog_logs")
		if err != nil {
			if !wasPaused {
				ic.Resume()
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		InvalidateAllCaches()
		slog.Info("database truncated", "count", count)

		logFilePath := os.Getenv("LOG_FILE_PATH")
		if logFilePath == "" {
			logFilePath = "/data/logs.jsonl"
		}
		if err := os.Truncate(logFilePath, 0); err != nil {
			slog.Error("failed to truncate log file", "path", logFilePath, "error", err)
		} else {
			slog.Info("log file truncated", "path", logFilePath)
		}

		if req.PauseDuringPurge && !wasPaused {
			ic.Resume()
			slog.Info("ingestion resumed")
		}

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "logs_purged", c.ClientIP(), fmt.Sprintf("purged %d log entries", count))
		c.JSON(http.StatusOK, gin.H{
			"message":       "All logs purged",
			"deleted_count": count,
		})
	}
}

type TestLDAPRequest struct {
	Server       string `json:"server"`
	Port         int    `json:"port"`
	UseTLS       bool   `json:"use_tls"`
	VerifyCert   bool   `json:"verify_cert"`
	CaCert       string `json:"ca_cert"`
	BaseDN       string `json:"base_dn"`
	BindDN       string `json:"bind_dn"`
	BindPassword string `json:"bind_password"`
}

func TestLDAP(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req TestLDAPRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if req.Port == 0 {
			req.Port = 389
		}

		err := ldap.TestConnection(req.Server, req.Port, req.UseTLS, req.VerifyCert, req.CaCert, req.BaseDN, req.BindDN, req.BindPassword)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Successfully connected to %s:%d", req.Server, req.Port)})
	}
}

func GetAuditLog(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := DefaultAdminLimit
		if l := c.Query("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 1000 {
				limit = n
			}
		}

		rows, err := database.Query(
			"SELECT id, user_id, username, action, ip, details, created_at FROM audit_log ORDER BY created_at DESC LIMIT $1",
			limit,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()

		var logs []db.AuditLog
		for rows.Next() {
			var a db.AuditLog
			if err := rows.Scan(&a.ID, &a.UserID, &a.Username, &a.Action, &a.IP, &a.Details, &a.CreatedAt); err != nil {
				continue
			}
			logs = append(logs, a)
		}

		c.JSON(http.StatusOK, logs)
	}
}

func PauseIngestion(ic control.IngestionController) gin.HandlerFunc {
	return func(c *gin.Context) {
		ic.Pause()
		c.JSON(http.StatusOK, gin.H{"message": "Ingestion paused", "paused": true})
	}
}

func ResumeIngestion(ic control.IngestionController) gin.HandlerFunc {
	return func(c *gin.Context) {
		ic.Resume()
		c.JSON(http.StatusOK, gin.H{"message": "Ingestion resumed", "paused": false})
	}
}

func GetIngestionStatus(ic control.IngestionController) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"paused": ic.IsPaused()})
	}
}

func GetSlowQueries() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, GetSlowQueryRecords())
	}
}

func ClearSlowQueriesHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		ClearSlowQueries()
		c.JSON(http.StatusOK, gin.H{"message": "Slow query log cleared"})
	}
}
