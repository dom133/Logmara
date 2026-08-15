package handler

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"logmara/audit"
	"logmara/auth"
	"logmara/control"
	"logmara/db"
	"logmara/ldap"
	"logmara/middleware"
	"logmara/model"
	"logmara/tailer"

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

func ListUsers(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		users, err := db.GetAllUsers(database)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("admin.usersListFailed", "Failed to list users", err))
			return
		}
		c.JSON(http.StatusOK, users)
	}
}

// ListUserDirectory returns just {id, username} for every user - enough to
// populate a "pick a user" control (e.g. the in_app/push notification
// channel's target-user selector) without exposing the account management
// data (email, role, lockout status, ...) that GET /admin/users carries and
// which is why that endpoint stays admin-only. Available to admin and
// editor - anyone who can create a notification channel needs this.
func ListUserDirectory(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		users, err := db.GetUserDirectory(database)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("admin.usersListFailed", "Failed to list users", err))
			return
		}
		c.JSON(http.StatusOK, users)
	}
}

func CreateUser(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		var req CreateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequestBody", "Invalid request body", err))
			return
		}

		if !isValidRole(req.Role) {
			middleware.HandleError(c, model.NewBadRequestKey("admin.invalidRole", "Invalid role. Must be admin, editor, or viewer", nil))
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
				middleware.HandleError(c, model.NewBadRequestKey("admin.ldapUserCreationFailed", "Failed to create LDAP user", err))
				return
			}
			actorID, actorName := actorFromContext(c)
			audit.LogAudit(database, actorID, actorName, "user_created", c.ClientIP(), fmt.Sprintf("created %s user %s", authType, req.Username))
			c.JSON(http.StatusCreated, user)
			return
		}

		if req.Password == "" || len(req.Password) < 8 {
			middleware.HandleError(c, model.NewBadRequestKey("admin.passwordRequired", "Password is required and must be at least 8 characters", nil))
			return
		}
		policy := auth.LoadPasswordPolicy(func(k, def string) string { return db.GetSetting(database, k, def) })
		if err := auth.ValidatePasswordWithPolicy(policy, req.Password); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("admin.passwordRequirements", "Password does not meet requirements", err))
			return
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("admin.hashFailed", "Could not hash password", err))
			return
		}

		isAdmin := req.Role == RoleAdmin
		user, err := db.CreateUser(database, req.Username, hash, req.Email, isAdmin, req.Role)
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("admin.userCreationFailed", "Failed to create user", err))
			return
		}

		_ = db.AddPasswordHistory(database, user.ID, hash)

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "user_created", c.ClientIP(), fmt.Sprintf("created local user %s", req.Username))
		c.JSON(http.StatusCreated, user)
	}
}

func UpdateUser(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidUserID", "Invalid user ID", nil))
			return
		}

		var req UpdateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequestBody", "Invalid request body", err))
			return
		}

		if req.Role != nil && !isValidRole(*req.Role) {
			middleware.HandleError(c, model.NewBadRequestKey("admin.invalidRole", "Invalid role", nil))
			return
		}

		oldUser, err := db.GetUserByID(database, id)
		if err != nil {
			middleware.HandleError(c, model.NewNotFoundKey("admin.userNotFound", "User not found", err))
			return
		}

		user, err := db.UpdateUser(database, id, req.Role, req.IsActive)
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("admin.userUpdateFailed", "Failed to update user", err))
			return
		}

		actorID, actorName := actorFromContext(c)
		var changes []string
		if req.Role != nil && oldUser.Role != *req.Role {
			changes = append(changes, fmt.Sprintf("role: %s -> %s", oldUser.Role, *req.Role))
		}
		if req.IsActive != nil && oldUser.IsActive != *req.IsActive {
			changes = append(changes, fmt.Sprintf("is_active: %v -> %v", oldUser.IsActive, *req.IsActive))
		}
		details := fmt.Sprintf("updated user %s", user.Username)
		if len(changes) > 0 {
			details += fmt.Sprintf(" | changes: %s", strings.Join(changes, ", "))
		}
		audit.LogAudit(database, actorID, actorName, "user_updated", c.ClientIP(), details)
		c.JSON(http.StatusOK, user)
	}
}

func DeleteUser(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidUserID", "Invalid user ID", nil))
			return
		}

		if err := db.DeleteUser(database, id); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("admin.userDeleteFailed", "Failed to delete user", err))
			return
		}

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "user_deleted", c.ClientIP(), fmt.Sprintf("deleted user id %d", id))
		c.JSON(http.StatusOK, gin.H{"message": "User deleted"})
	}
}

func ResetPassword(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidUserID", "Invalid user ID", nil))
			return
		}

		var req ResetPasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequestBody", "Invalid request body", err))
			return
		}
		policy := auth.LoadPasswordPolicy(func(k, def string) string { return db.GetSetting(database, k, def) })
		if err := auth.ValidatePasswordWithPolicy(policy, req.Password); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("admin.passwordRequirements", "Password does not meet requirements", err))
			return
		}

		var authType string
		if err := database.QueryRow("SELECT auth_type FROM users WHERE id = $1", id).Scan(&authType); err != nil {
			middleware.HandleError(c, model.NewNotFoundKey("admin.userNotFound", "User not found", err))
			return
		}
		if authType == "ldap" {
			middleware.HandleError(c, model.NewBadRequestKey("admin.ldapPasswordResetNotAllowed", "Cannot reset password for LDAP users", nil))
			return
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("admin.hashFailed", "Could not hash password", err))
			return
		}

		if err := db.ResetUserPassword(database, id, hash); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("admin.passwordResetFailed", "Failed to reset password", err))
			return
		}

		_ = db.AddPasswordHistory(database, id, hash)
		_ = db.TrimPasswordHistory(database, id)

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "password_reset_by_admin", c.ClientIP(), fmt.Sprintf("reset password for user id %d", id))
		c.JSON(http.StatusOK, gin.H{"message": "Password reset successful"})
	}
}

func UnlockUserHandler(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidUserID", "Invalid user ID", nil))
			return
		}

		if err := db.UnlockUser(database, id); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("admin.unlockFailed", "Failed to unlock user", err))
			return
		}

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "user_unlocked", c.ClientIP(), fmt.Sprintf("unlocked user id %d", id))
		c.JSON(http.StatusOK, gin.H{"message": "User unlocked"})
	}
}

func GetSettings(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		settings, err := db.GetAllSettings(database)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("admin.settingsLoadFailed", "Failed to get settings", err))
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

func UpdateSettings(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		var settings map[string]string
		if err := c.ShouldBindJSON(&settings); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequestBody", "Invalid request body", err))
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
			middleware.HandleError(c, model.NewBadRequestKey("admin.sslCertNotFound", "Cannot enable HTTPS: SSL certificate not found. Please upload certificate and key first.", nil))
			return
		}
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			middleware.HandleError(c, model.NewBadRequestKey("admin.sslKeyNotFound", "Cannot enable HTTPS: SSL private key not found. Please upload certificate and key first.", nil))
				return
			}
		}

		// Capture old values BEFORE updating
		oldValues := make(map[string]string)
		for k := range settings {
			oldValues[k] = db.GetSetting(database, k, "<unset>")
		}

		for k, v := range settings {
			if err := db.UpdateSetting(database, k, v); err != nil {
				middleware.HandleError(c, model.NewBadRequestKey("admin.settingUpdateFailed", "Failed to update setting: "+k, err))
				return
			}
		}

		// List of sensitive setting keys - never log their values
		sensitiveKeys := map[string]bool{
			"smtp_password": true, "ldap_bind_password": true,
			"jwt_secret": true, "encryption_key": true,
			"db_password": true, "redis_password": true,
		}

		// Build audit details showing what changed
		var changes []string
		for k, v := range settings {
			oldVal := oldValues[k]
			if oldVal != v {
				var change string
				if sensitiveKeys[k] {
					change = fmt.Sprintf("%s: (value hidden)", k)
				} else {
					change = fmt.Sprintf("%s: %q -> %q", k, oldVal, v)
				}
				changes = append(changes, change)
			}
		}
		auditDetails := ""
		if len(changes) > 0 {
			auditDetails = strings.Join(changes, "; ")
		}

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "settings_updated", c.ClientIP(), auditDetails)

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
					"nginx_reload_error": "Nginx reload failed",
				})
				return
			}
		}

		if newRelayEnabled != "" && newRelayEnabled != oldRelayEnabled {
			if err := SyncRelayConfig(pool); err != nil {
				slog.Warn("relay config sync failed after settings update", "error", err)
				c.JSON(http.StatusOK, gin.H{
					"message":            "Settings updated",
					"relay_reload_error": "Relay config sync failed",
				})
				return
			}
		}

		c.JSON(http.StatusOK, gin.H{"message": "Settings updated"})
	}
}

// httpsProxyProtocolTrustedCIDRs lists the ranges nginx will trust a PROXY
// protocol header from when NGINX_PROXY_PROTOCOL is enabled - mirrors
// main.go's defaultTrustedProxies (same "private network this app is
// deployed on" trust boundary, just enforced at the nginx layer instead of
// Gin's, since here the PROXY protocol comes from haproxy-app rather than
// nginx forwarding a header to Gin directly).
var httpsProxyProtocolTrustedCIDRs = []string{
	"127.0.0.1/32", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7",
}

// nginxProxyProtocolEnabled reports whether nginx's HTTPS listener should
// expect a PROXY protocol v1 header ahead of the TLS handshake instead of
// terminating TLS directly against the client. Needed only for the HA stack
// (docker-stack.app.yml): haproxy-app's frontend_https listener is a raw TCP
// passthrough (TLS terminates at nginx, so HAProxy can't rewrite HTTP
// headers the way it does for :80's forwardfor) and uses send-proxy to
// convey the real client IP instead - see haproxy/haproxy-app.cfg. Left off
// (the default) for plain docker-compose.yml, where nginx receives HTTPS
// directly from the client and a PROXY protocol header would just break the
// handshake.
func nginxProxyProtocolEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("NGINX_PROXY_PROTOCOL")), "true")
}

// httpsServerBlockTemplate is the nginx 443 server block, written verbatim
// to https.conf whenever https_enabled is on. It mirrors the :80 server
// block in frontend/nginx.conf. %%LISTEN%%, %%REAL_IP%%, and %%API_UPSTREAM%%
// are filled in by httpsServerBlock() - see nginxProxyProtocolEnabled for why.
const httpsServerBlockTemplate = `server {
    %%LISTEN%%
    server_name localhost;

%%REAL_IP%%    ssl_certificate /data/ssl/server.crt;
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
        proxy_pass %%API_UPSTREAM%%;
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
        proxy_pass %%API_UPSTREAM%%;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
`

// httpsServerBlock fills httpsServerBlockTemplate's %%LISTEN%%,
// %%REAL_IP%%, and %%API_UPSTREAM%% placeholders. With NGINX_PROXY_PROTOCOL
// unset (the docker-compose.yml default), both resolve to exactly the
// original plain-TLS block; with it set to "true" (docker-stack.app.yml),
// nginx additionally expects and trusts a PROXY protocol header from
// httpsProxyProtocolTrustedCIDRs - see nginxProxyProtocolEnabled.
func httpsServerBlock() string {
	listen := "listen 443 ssl;"
	realIP := ""
	if nginxProxyProtocolEnabled() {
		listen = "listen 443 ssl proxy_protocol;"
		var b strings.Builder
		for _, cidr := range httpsProxyProtocolTrustedCIDRs {
			b.WriteString("    set_real_ip_from " + cidr + ";\n")
		}
		b.WriteString("    real_ip_header proxy_protocol;\n\n")
		realIP = b.String()
	}
	apiUpstream := os.Getenv("API_UPSTREAM")
	if apiUpstream == "" {
		apiUpstream = "http://api:8080"
	}
	block := strings.Replace(httpsServerBlockTemplate, "%%LISTEN%%", listen, 1)
	block = strings.Replace(block, "%%REAL_IP%%", realIP, 1)
	return strings.Replace(block, "%%API_UPSTREAM%%", apiUpstream, -1)
}

// corsMapDirective renders the nginx `map` block that resolves the
// request's Origin header to an Access-Control-Allow-Origin value.
// Wildcard "*" is rejected (treated as empty) to prevent unrestricted CORS.
// An empty list allows none. This is the only CORS enforcement in the app -
// clients only ever reach the API through this nginx proxy, so there's
// nothing equivalent on the backend side.
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
			continue
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
		httpsConf = httpsServerBlock()
	}
	if err := os.WriteFile(filepath.Join(confDir, "https.conf"), []byte(httpsConf), 0644); err != nil {
		return fmt.Errorf("write https.conf: %w", err)
	}

	redirectConf := ""
	if httpsEnabled && redirectEnabled {
		// /healthz is exempted: this fires in nginx's server-rewrite phase,
		// before location matching, so an unconditional return here would
		// 301 haproxy-app's health probe (GET / on :80, "option httpchk" in
		// haproxy/haproxy-app.cfg's frontend_http listener) right along with
		// real traffic. That check requires a plain 200, so a 301 marks
		// every frontend replica DOWN and haproxy starts answering :80 with
		// 503 instead of ever reaching nginx to redirect it. See the
		// `location = /healthz` block in frontend/nginx.conf, which this
		// exemption leaves reachable to satisfy the health check.
		redirectConf = "if ($request_uri != \"/healthz\") {\n    return 301 https://$host$request_uri;\n}\n"
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

// reloadNginxWithRetry retries reloadNginx with exponential backoff,
// smoothing over the brief window (container startup, or an admin action
// that races it) where the frontend's reload sidecar isn't listening yet.
func reloadNginxWithRetry(httpsEnabled, redirectEnabled bool, corsOrigins string, attempts int, delay time.Duration) error {
	var err error
	backoff := delay
	for i := 0; i < attempts; i++ {
		if err = reloadNginx(httpsEnabled, redirectEnabled, corsOrigins); err == nil {
			return nil
		}
		if i < attempts-1 {
			time.Sleep(backoff)
			backoff = backoff * 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
	return err
}

// ReloadNginx re-applies the current HTTPS/redirect/CORS settings and
// triggers an nginx config reload via the frontend container's sidecar.
func ReloadNginx(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := SyncNginxHTTPS(pool); err != nil {
			middleware.HandleError(c, model.NewInternalKey("admin.nginxReloadFailed", "Failed to reload nginx", err))
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
func SyncNginxHTTPS(pool *db.DynamicPool) error {
	return SyncNginxHTTPSWithRetry(pool, 5, 2*time.Second)
}

// SyncNginxHTTPSWithRetry is SyncNginxHTTPS with a caller-chosen retry
// budget - used at startup with a much larger budget than the interactive
// endpoints, since a cold `docker compose up` can leave the frontend
// container down for a while and there's no request to keep fast there.
func SyncNginxHTTPSWithRetry(pool *db.DynamicPool, attempts int, delay time.Duration) error {
	database := pool.Get()
	httpsEnabled := db.GetSetting(database, "https_enabled", "false") == "true"
	redirectEnabled := db.GetSetting(database, "https_redirect", "false") == "true"
	corsOrigins := db.GetSetting(database, "cors_origins", "")
	return reloadNginxWithRetry(httpsEnabled, redirectEnabled, corsOrigins, attempts, delay)
}

func CleanupLogs(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		// On a large table this can run well past the server's default 15s
		// WriteTimeout (see main.go) - the batched delete alone can take
		// minutes. Same fix as StreamNotifications: lift the per-connection
		// write deadline so a slow cleanup doesn't get its response write
		// silently killed out from under it.
		_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})

		retentionDays := 30
		if val := db.GetSetting(database, "retention_days", "30"); val != "" {
			if d, err := strconv.Atoi(val); err == nil {
				retentionDays = d
			}
		}

		deleted, err := db.CleanupOldLogs(database, retentionDays)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("admin.cleanupFailed", "Failed to cleanup logs", err))
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

func PurgeAllLogs(pool *db.DynamicPool, ic control.IngestionController) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		// TRUNCATE needs an ACCESS EXCLUSIVE lock and can sit queued behind
		// an in-progress VACUUM/MV refresh/long transaction on syslog_logs
		// well past the server's default 15s WriteTimeout (see main.go).
		// Same fix as StreamNotifications: lift the per-connection write
		// deadline so that wait doesn't kill the response write.
		_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})

		var req PurgeRequest
		_ = c.ShouldBindJSON(&req)

		wasPaused := ic.IsPaused()
		if req.PauseDuringPurge {
			ic.Pause()
			// Give workers time to NACK in-flight deliveries back to the queue.
			// With per-delivery pause check (worker.go) this propagates quickly.
			time.Sleep(2 * time.Second)
		}

		row := database.QueryRow("SELECT COUNT(*) FROM syslog_logs")
		var count int
		row.Scan(&count)
		slog.Info("purge started", "count", count)

		_, err := pool.Get().Exec("TRUNCATE TABLE syslog_logs")
		if err != nil {
			if !wasPaused {
				ic.Resume()
			}
			middleware.HandleError(c, model.NewInternalKey("admin.purgeFailed", "Failed to purge logs", err))
			return
		}
		InvalidateAllCaches()
		slog.Info("database truncated", "count", count)

		// A full purge is the one point a PARTITION_INTERVAL change is safe
		// to actually take effect: db.activePartitionGranularity otherwise
		// keeps a running database on whatever granularity its existing
		// partitions already show (to avoid overlap errors from switching
		// mid-flight - see db/maintenance.go), so those old, now-empty date
		// partitions need dropping here or that inference would just find
		// them again and stay locked to the old granularity forever.
		// syslog_logs_default (not matched by this pattern) is left as the
		// catch-all until EnsurePartitions below recreates real ones.
		partRows, partErr := pool.Get().Query(`
			SELECT c.relname
			FROM pg_inherits i
			JOIN pg_class c ON c.oid = i.inhrelid
			JOIN pg_class p ON p.oid = i.inhparent
			WHERE p.relname = 'syslog_logs' AND c.relname ~ '^syslog_logs_\d{4}_\d{2}(_\d{2})?$'
		`)
		if partErr != nil {
			slog.Error("purge: failed to list date partitions", "error", partErr)
		} else {
			var partitionNames []string
			for partRows.Next() {
				var name string
				if partRows.Scan(&name) == nil {
					partitionNames = append(partitionNames, name)
				}
			}
			partRows.Close()
			for _, name := range partitionNames {
				if _, err := pool.Get().Exec("DROP TABLE IF EXISTS " + name); err != nil {
					slog.Error("purge: failed to drop date partition", "partition", name, "error", err)
				}
			}
			if len(partitionNames) > 0 {
				slog.Info("purge: dropped date partitions so any PARTITION_INTERVAL change takes effect", "count", len(partitionNames))
				go db.EnsurePartitions(database)
			}
		}

		// Wait for the RabbitMQ queue depth to stabilise after pause.
		// Workers NACK/requeue in-flight deliveries; the reader stops
		// publishing. Poll until the queue stops growing or timeout.
		if req.PauseDuringPurge {
			initialDepth := tailer.GetTailerQueueLength()
			stable := false
			for i := 0; i < 10; i++ {
				time.Sleep(200 * time.Millisecond)
				depth := tailer.GetTailerQueueLength()
				if depth < initialDepth-50 || depth < 50 {
					stable = true
					break
				}
			}
			if !stable {
				slog.Warn("purge: queue did not fully drain before purge",
					"initial_depth", initialDepth, "final_depth", tailer.GetTailerQueueLength())
			} else {
				slog.Info("purge: queue stabilised before purge",
					"initial_depth", initialDepth, "final_depth", tailer.GetTailerQueueLength())
			}
		}

		// Purge RabbitMQ ingestion queue to drop in-flight messages.
		queuePurged, purgeErr := tailer.PurgeTailerQueue()
		if purgeErr != "" {
			slog.Error("purge: tailer queue purge failed", "error", purgeErr)
			if req.PauseDuringPurge && !wasPaused {
				ic.Resume()
			}
			middleware.HandleError(c, model.NewInternalKey("admin.purgeQueueFailed", "Failed to purge RabbitMQ queue", errors.New(purgeErr)))
			return
		}
		slog.Info("purge: tailer queue purged", "messages_removed", queuePurged)

		logFilePath := os.Getenv("LOG_FILE_PATH")
		if logFilePath == "" {
			logFilePath = "/data/logs.jsonl"
		}
		// Use atomic file swap instead of os.Truncate. Rsyslog holds a long-lived
		// file descriptor on logs.jsonl — truncating in place while rsyslog
		// concurrently appends can leave the file with its original size on disk
		// because the kernel keeps the fd offset past the truncated region.
		// Creating an empty tmp file, renaming it over logs.jsonl, then sending
		// SIGHUP to rsyslog (ReopenRsyslogLogFile) forces rsyslog to reopen the
		// new, empty inode — same pattern as tailer.compactFile.
		tmpPath := logFilePath + ".purge.tmp"
		tmpFile, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			slog.Error("failed to create empty log file", "path", tmpPath, "error", err)
		} else {
			tmpFile.Close()
			if err := os.Rename(tmpPath, logFilePath); err != nil {
				slog.Error("failed to swap log file", "path", logFilePath, "error", err)
				os.Remove(tmpPath)
			} else {
				slog.Info("log file replaced with empty file", "path", logFilePath)
				if err := ReopenRsyslogLogFile(); err != nil {
					slog.Error("failed to ask rsyslog to reopen log file after purge", "error", err)
				}
			}
		}

		// Clear tailer position checkpoint so the tailer restarts from 0
		posFile := filepath.Join(filepath.Dir(logFilePath), ".tailer_pos")
		if err := os.Remove(posFile); err != nil && !os.IsNotExist(err) {
			slog.Error("failed to remove position file", "path", posFile, "error", err)
		} else {
			slog.Info("position file removed", "path", posFile)
		}
		tailer.ResetTailerPosition()

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

func TestLDAP(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req TestLDAPRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequestBody", "Invalid request body", err))
			return
		}

		if req.Port == 0 {
			req.Port = 389
		}

		err := ldap.TestConnection(req.Server, req.Port, req.UseTLS, req.VerifyCert, req.CaCert, req.BaseDN, req.BindDN, req.BindPassword)
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("admin.ldapTestFailed", "LDAP connection test failed", err))
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Successfully connected to %s:%d", req.Server, req.Port)})
	}
}

type AuditLogRequest struct {
	Limit int `json:"limit"`
}

func GetAuditLog(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req AuditLogRequest
		_ = c.ShouldBindJSON(&req)
		limit := req.Limit
		if limit <= 0 || limit > 1000 {
			limit = DefaultAdminLimit
		}

		rows, err := pool.Get().Query(
			"SELECT id, user_id, username, action, ip, details, created_at FROM audit_log ORDER BY created_at DESC LIMIT $1",
			limit,
		)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("admin.auditLogFailed", "Failed to fetch audit log", err))
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

type AuditLogQueryRequest struct {
	Username string `json:"username"`
	Action   string `json:"action"`
	From     string `json:"from"`
	To       string `json:"to"`
	Limit    int    `json:"limit"`
	Offset   int    `json:"offset"`
}

func GetAuditLogsHandler(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		var req AuditLogQueryRequest
		_ = c.ShouldBindJSON(&req)
		limit := req.Limit
		if limit <= 0 || limit > 1000 {
			limit = 50
		}
		offset := req.Offset
		if offset < 0 {
			offset = 0
		}

		filter := db.AuditLogFilter{
			Username: req.Username,
			Action:   req.Action,
			From:     req.From,
			To:       req.To,
			Limit:    limit,
			Offset:   offset,
		}

		logs, total, err := db.GetAuditLogs(database, filter)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("admin.auditLogsFailed", "Failed to fetch audit logs", err))
			return
		}

		if logs == nil {
			logs = []db.AuditLog{}
		}

		c.JSON(http.StatusOK, gin.H{
			"data":  logs,
			"total": total,
		})
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

func GetTailerMetrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		agg := tailer.GetTailerMetricsAggregated()
		if agg == nil {
			c.JSON(http.StatusOK, gin.H{
				"pipeline_active": false,
				"metrics":         nil,
				"replicas":        nil,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"pipeline_active": agg.PipelineActive,
			"metrics":         agg,
			"replicas":        agg.Replicas,
		})
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
