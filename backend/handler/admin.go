package handler

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"syslog-gui/auth"
	"syslog-gui/control"
	"syslog-gui/db"
	"syslog-gui/ldap"

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
		for _, k := range []string{"jwt_secret", "encryption_key", "ldap_bind_password"} {
			if v, ok := settings[k]; ok && v != "" {
				settings[k] = "****"
			}
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

		for k, v := range settings {
			if err := db.UpdateSetting(database, k, v); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to update setting: " + k})
				return
			}
		}

		c.JSON(http.StatusOK, gin.H{"message": "Settings updated"})
	}
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

func PurgeAllLogs(database *sql.DB, ic *control.IngestionController) gin.HandlerFunc {
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

func PauseIngestion(ic *control.IngestionController) gin.HandlerFunc {
	return func(c *gin.Context) {
		ic.Pause()
		c.JSON(http.StatusOK, gin.H{"message": "Ingestion paused", "paused": true})
	}
}

func ResumeIngestion(ic *control.IngestionController) gin.HandlerFunc {
	return func(c *gin.Context) {
		ic.Resume()
		c.JSON(http.StatusOK, gin.H{"message": "Ingestion resumed", "paused": false})
	}
}

func GetIngestionStatus(ic *control.IngestionController) gin.HandlerFunc {
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
