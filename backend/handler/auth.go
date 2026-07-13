package handler

import (
	"database/sql"
	"log"
	"net/http"
	"time"

	"syslog-gui/auth"
	"syslog-gui/db"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	User         db.User `json:"user"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=8"`
}

func Login(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
			return
		}

		var user db.User
		err := database.QueryRow(
			"SELECT id, username, password_hash, role, is_admin, is_active, created_at FROM users WHERE username = $1",
			req.Username,
		).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.IsAdmin, &user.IsActive, &user.CreatedAt)

		if err != nil || !user.IsActive {
			dummyHash := "$2b$14$AAAAAAAAAAAAAAAAAAAAAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(req.Password))
			logAudit(database, 0, req.Username, "login_failed", c.ClientIP(), "invalid user or inactive")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
			logAudit(database, user.ID, user.Username, "login_failed", c.ClientIP(), "wrong password")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
			return
		}

		token, err := auth.GenerateToken(user.Username, user.Role)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
			return
		}

		refreshToken, expiresAt := auth.GenerateRefreshToken(int(user.ID))
		if err := insertRefreshToken(database, int(user.ID), refreshToken, expiresAt); err != nil {
			log.Printf("Failed to store refresh token: %v", err)
		}

		logAudit(database, user.ID, user.Username, "login_success", c.ClientIP(), "")

		c.JSON(http.StatusOK, LoginResponse{
			Token:        token,
			RefreshToken: refreshToken,
			User:         user,
		})
	}
}

func Refresh(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RefreshRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
			return
		}

		var userID int
		var expiresAt time.Time
		err := database.QueryRow(
			"SELECT user_id, expires_at FROM refresh_tokens WHERE token = $1 AND expires_at > NOW() AND used = false",
			req.RefreshToken,
		).Scan(&userID, &expiresAt)

		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired refresh token"})
			return
		}

		database.Exec("UPDATE refresh_tokens SET used = true WHERE token = $1", req.RefreshToken)

		var user db.User
		if err := database.QueryRow(
			"SELECT id, username, password_hash, role, is_admin, is_active, created_at FROM users WHERE id = $1",
			userID,
		).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Role, &user.IsAdmin, &user.IsActive, &user.CreatedAt); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
			return
		}

		token, err := auth.GenerateToken(user.Username, user.Role)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
			return
		}

		newRefreshToken, newExpiresAt := auth.GenerateRefreshToken(userID)
		if err := insertRefreshToken(database, userID, newRefreshToken, newExpiresAt); err != nil {
			log.Printf("Failed to store refresh token: %v", err)
		}

		c.JSON(http.StatusOK, gin.H{
			"token":         token,
			"refresh_token": newRefreshToken,
		})
	}
}

func Logout(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RefreshRequest
		if err := c.ShouldBindJSON(&req); err == nil {
			database.Exec("UPDATE refresh_tokens SET used = true WHERE token = $1", req.RefreshToken)
		}
		c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
	}
}

func GetMe() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get("claims")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			return
		}

		mapClaims := claims.(*jwt.MapClaims)
		username := (*mapClaims)["username"].(string)
		role := (*mapClaims)["role"].(string)

		c.JSON(http.StatusOK, gin.H{
			"username": username,
			"role":     role,
		})
	}
}

func ChangePassword(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, _ := c.Get("claims")
		userClaims := claims.(*jwt.MapClaims)
		username := (*userClaims)["username"].(string)

		var req ChangePasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
			return
		}

		if len(req.NewPassword) < 8 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Password must be at least 8 characters"})
			return
		}

		var storedHash string
		err := database.QueryRow("SELECT password_hash FROM users WHERE username = $1", username).Scan(&storedHash)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(req.CurrentPassword)); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Current password is incorrect"})
			return
		}

		newHash, err := auth.HashPassword(req.NewPassword)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
			return
		}

		if err := db.ResetUserPassword(database, getUserID(database, username), newHash); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
			return
		}

		logAudit(database, getUserID(database, username), username, "password_changed", c.ClientIP(), "")
		c.JSON(http.StatusOK, gin.H{"message": "Password updated"})
	}
}

func insertRefreshToken(db *sql.DB, userID int, token string, expiresAt time.Time) error {
	_, err := db.Exec(
		"INSERT INTO refresh_tokens (user_id, token, expires_at, used) VALUES ($1, $2, $3, false)",
		userID, token, expiresAt,
	)
	return err
}

func getUserID(db *sql.DB, username string) int64 {
	var id int64
	db.QueryRow("SELECT id FROM users WHERE username = $1", username).Scan(&id)
	return id
}

func logAudit(db *sql.DB, userID int64, username, action, ip, details string) {
	_, err := db.Exec(
		"INSERT INTO audit_log (user_id, username, action, ip, details, created_at) VALUES ($1, $2, $3, $4, $5, NOW())",
		userID, username, action, ip, details,
	)
	if err != nil {
		log.Printf("Audit log error: %v", err)
	}
}