package handler

import (
	"database/sql"
	"net/http"
	"strconv"

	"syslog-gui/auth"
	"syslog-gui/db"

	"github.com/gin-gonic/gin"
)

type CreateUserRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required,min=4"`
	Role     string `json:"role" binding:"required"`
}

type UpdateUserRequest struct {
	Role     *string `json:"role"`
	IsActive *bool   `json:"is_active"`
}

type ResetPasswordRequest struct {
	Password string `json:"password" binding:"required,min=4"`
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

		validRoles := []string{"admin", "editor", "viewer"}
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

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not hash password"})
			return
		}

		isAdmin := req.Role == "admin"
		user, err := db.CreateUser(database, req.Username, hash, isAdmin, req.Role)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, user)
	}
}

func UpdateUser(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
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
			validRoles := []string{"admin", "editor", "viewer"}
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
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
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
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		var req ResetPasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

func PurgeAllLogs(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := database.Exec("DELETE FROM syslog_logs")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		count, _ := result.RowsAffected()

		c.JSON(http.StatusOK, gin.H{
			"message":       "All logs purged",
			"deleted_count": count,
		})
	}
}