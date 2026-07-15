package handler

import (
	"database/sql"
	"log"
	"net/http"
	"time"

	"syslog-gui/auth"
	"syslog-gui/db"
	"syslog-gui/ldap"

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

		var user *db.User

		existing, err := db.GetUserByUsername(database, req.Username)
		if err == nil && existing.IsActive {
			if err := bcrypt.CompareHashAndPassword([]byte(existing.PasswordHash), []byte(req.Password)); err == nil {
				user = existing
			}
		}

		if user == nil {
			dummyHash := "$2b$14$AAAAAAAAAAAAAAAAAAAAAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(req.Password))

			ldapCfg := ldap.LoadConfig(func(key, def string) string {
				return db.GetSetting(database, key, def)
			})

			if ldapCfg.Enabled {
				attrs, err := ldap.Authenticate(ldapCfg, req.Username, req.Password)
				if err != nil {
					log.Printf("LDAP auth error: %v", err)
				} else if attrs != nil {
					email := attrs["email"]
					username := attrs["username"]
					if username == "" {
						username = req.Username
					}

					existing, err := db.GetUserByUsername(database, username)
					if err != nil {
						if ldapCfg.AutoProvision {
							u, err := db.CreateLDAPUser(database, username, email, ldapCfg.DefaultRole, ldapCfg.DefaultRole == "admin")
							if err != nil {
								log.Printf("LDAP auto-provision failed: %v", err)
								logAudit(database, 0, req.Username, "login_failed", c.ClientIP(), "auto-provision failed")
								c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
								return
							}
							user = u
						} else {
							logAudit(database, 0, req.Username, "login_failed", c.ClientIP(), "user not found locally")
							c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
							return
						}
					} else {
						user = existing
					}
				}
			}

			if user == nil {
				logAudit(database, 0, req.Username, "login_failed", c.ClientIP(), "invalid user or inactive")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
				return
			}
		}

		db.UpdateLastLogin(database, user.Username)
		user.LastLoginAt = ptrTime(time.Now())

		token, err := auth.GenerateToken(user.ID, user.Username, user.Role)
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
			User:         *user,
		})
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
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
			"SELECT id, username, email, password_hash, role, is_admin, is_active, created_at, last_login_at FROM users WHERE id = $1",
			userID,
		).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.Role, &user.IsAdmin, &user.IsActive, &user.CreatedAt, &user.LastLoginAt); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
			return
		}

		token, err := auth.GenerateToken(user.ID, user.Username, user.Role)
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

		userID := int((*mapClaims)["user_id"].(float64))
		c.JSON(http.StatusOK, gin.H{
			"id":       userID,
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