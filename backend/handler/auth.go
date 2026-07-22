package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"syslytics/audit"
	"syslytics/auth"
	"syslytics/db"
	"syslytics/ldap"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	AccessTokenCookieName  = "accessToken"
	RefreshTokenCookieName = "refreshToken"
	CSRFTokenCookieName    = "csrf_token"
)

func setAuthCookies(c *gin.Context, accessToken, refreshToken string, accessExpiry, refreshExpiry time.Time) {
	csrf := generateCSRFToken()
	accessMaxAge := int(time.Until(accessExpiry).Seconds())
	refreshMaxAge := int(time.Until(refreshExpiry).Seconds())
	secure := c.GetHeader("X-Forwarded-Proto") == "https"

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     CSRFTokenCookieName,
		Value:    csrf,
		Path:     "/",
		Expires:  accessExpiry,
		MaxAge:   accessMaxAge,
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     AccessTokenCookieName,
		Value:    accessToken,
		Path:     "/",
		Expires:  accessExpiry,
		MaxAge:   accessMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     RefreshTokenCookieName,
		Value:    refreshToken,
		Path:     "/",
		Expires:  refreshExpiry,
		MaxAge:   refreshMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearAuthCookies(c *gin.Context) {
	secure := c.GetHeader("X-Forwarded-Proto") == "https"
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     CSRFTokenCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     AccessTokenCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     RefreshTokenCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

type LoginRequest struct {
	Username string `json:"username" binding:"required,max=100"`
	Password string `json:"password" binding:"required,max=128"`
}

type LoginResponse struct {
	Token        string  `json:"token"`
	RefreshToken string  `json:"refresh_token"`
	User         db.User `json:"user"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required,max=512"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required,max=128"`
	NewPassword     string `json:"new_password" binding:"required,min=8,max=128"`
}

func Login(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
			return
		}

		existing, err := db.GetUserByUsername(database, req.Username)
		if err == nil {
			if locked, _ := db.CheckUserLockout(database, existing.ID); locked && existing.IsActive {
				audit.LogAudit(database, existing.ID, req.Username, "login_failed_lockout", c.ClientIP(), "account locked")
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "Account temporarily locked. Try again later."})
				return
			}
		}

		var user *db.User

		existing, err = db.GetUserByUsername(database, req.Username)
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
					slog.Error("ldap auth error", "error", err)
				} else if attrs != nil {
					email := attrs["email"]
					username := attrs["username"]
					if username == "" {
						username = req.Username
					}

					existing, err := db.GetUserByUsername(database, username)
					if err != nil {
						if ldapCfg.AutoProvision {
							u, err := db.CreateLDAPUser(database, username, email, ldapCfg.DefaultRole, ldapCfg.DefaultRole == RoleAdmin)
							if err != nil {
								slog.Error("ldap auto-provision failed", "error", err)
								audit.LogAudit(database, 0, req.Username, "login_failed", c.ClientIP(), "auto-provision failed")
								c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
								return
							}
							user = u
						} else {
							audit.LogAudit(database, 0, req.Username, "login_failed", c.ClientIP(), "user not found locally")
							c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
							return
						}
					} else {
						user = existing
					}
				}
			}

			if user == nil {
				if existing != nil {
					db.IncrementFailedLogins(database, existing.ID)
				}
				audit.LogAudit(database, 0, req.Username, "login_failed", c.ClientIP(), "invalid user or inactive")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
				return
			}
		}

		db.ResetFailedLogins(database, user.ID)
		db.UpdateLastLogin(database, user.Username)
		user.LastLoginAt = ptrTime(time.Now())

		token, jti, accessExpiresAt, err := auth.GenerateToken(user.ID, user.Username, user.Role)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
			return
		}
		_ = jti

		refreshToken, refreshExpiresAt := auth.GenerateRefreshToken(int(user.ID))
		if err := insertRefreshToken(database, int(user.ID), refreshToken, refreshExpiresAt); err != nil {
			slog.Error("failed to store refresh token", "error", err)
		}

		audit.LogAudit(database, user.ID, user.Username, "login_success", c.ClientIP(), "")

		go db.RefreshMV(database)

		setAuthCookies(c, token, refreshToken, accessExpiresAt, refreshExpiresAt)

		c.JSON(http.StatusOK, gin.H{
			"user":       *user,
			"expires_at": accessExpiresAt.Unix(),
		})
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func getJWTExpiryMin() int {
	timeoutStr := os.Getenv("SESSION_TIMEOUT_MIN")
	if timeoutStr != "" {
		if t, err := strconv.Atoi(timeoutStr); err == nil && t > 0 {
			return t
		}
	}
	return 15
}

// refreshReuseGraceWindow tolerates a refresh token being presented twice in
// quick succession (e.g. two open tabs racing to refresh the same stored
// token). The first request rotates the token atomically; a second request
// arriving within this window is handed the token that already won the race
// instead of being logged out. Reuse outside this window is treated as a
// real replay and rejected.
const refreshReuseGraceWindow = 10 * time.Second

func Refresh(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		refreshToken, err := c.Cookie(RefreshTokenCookieName)
		if err != nil || refreshToken == "" {
			var req RefreshRequest
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
				return
			}
			refreshToken = req.RefreshToken
		}

		var userID int
		claimErr := database.QueryRow(
			`UPDATE refresh_tokens SET used = true, used_at = NOW()
			 WHERE token = $1 AND used = false AND expires_at > NOW()
			 RETURNING user_id`,
			refreshToken,
		).Scan(&userID)

		var replacementToken string
		if claimErr != nil {
			recoveredUserID, recoveredToken, recovered := recoverRacedRefresh(database, refreshToken)
			if !recovered {
				clearAuthCookies(c)
				audit.LogAudit(database, 0, "", "refresh_failed", c.ClientIP(), "invalid, expired, or reused refresh token")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired refresh token"})
				return
			}
			userID = recoveredUserID
			replacementToken = recoveredToken
		}

		user, err := getUserByID(database, userID)
		if err != nil {
			clearAuthCookies(c)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
			return
		}

		token, _, accessExpiresAt, err := auth.GenerateToken(user.ID, user.Username, user.Role)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
			return
		}

		if replacementToken != "" {
			audit.LogAudit(database, user.ID, user.Username, "refresh_race_recovered", c.ClientIP(), "")
			replacementExpiry := time.Now().Add(7 * 24 * time.Hour)
			setAuthCookies(c, token, replacementToken, accessExpiresAt, replacementExpiry)
			c.JSON(http.StatusOK, gin.H{"success": true, "expires_at": accessExpiresAt.Unix()})
			return
		}

		newRefreshToken, newExpiresAt := auth.GenerateRefreshToken(userID)
		if err := insertRefreshToken(database, userID, newRefreshToken, newExpiresAt); err != nil {
			slog.Error("failed to store refresh token", "error", err)
		}
		if _, err := database.Exec("UPDATE refresh_tokens SET replaced_by = $1 WHERE token = $2", newRefreshToken, refreshToken); err != nil {
			slog.Error("failed to link rotated refresh token", "error", err)
		}

		audit.LogAudit(database, user.ID, user.Username, "refresh_success", c.ClientIP(), "")

		setAuthCookies(c, token, newRefreshToken, accessExpiresAt, newExpiresAt)
		c.JSON(http.StatusOK, gin.H{"success": true, "expires_at": accessExpiresAt.Unix()})
	}
}

// recoverRacedRefresh checks whether the presented token was already
// rotated moments ago (used=true, linked to a replacement) and, if so,
// within the grace window, hands back the replacement token instead of
// treating this as a stale/replayed refresh token.
func recoverRacedRefresh(database *sql.DB, token string) (userID int, replacementToken string, ok bool) {
	var replacedBy sql.NullString
	var usedAt sql.NullTime
	err := database.QueryRow(
		"SELECT user_id, used_at, replaced_by FROM refresh_tokens WHERE token = $1 AND used = true",
		token,
	).Scan(&userID, &usedAt, &replacedBy)
	if err != nil || !replacedBy.Valid || !usedAt.Valid {
		return 0, "", false
	}
	if time.Since(usedAt.Time) > refreshReuseGraceWindow {
		return 0, "", false
	}

	var stillValid bool
	err = database.QueryRow(
		"SELECT expires_at > NOW() FROM refresh_tokens WHERE token = $1",
		replacedBy.String,
	).Scan(&stillValid)
	if err != nil || !stillValid {
		return 0, "", false
	}

	return userID, replacedBy.String, true
}

func getUserByID(database *sql.DB, userID int) (*db.User, error) {
	var user db.User
	err := database.QueryRow(
		"SELECT id, username, email, password_hash, role, is_admin, is_active, created_at, last_login_at FROM users WHERE id = $1",
		userID,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.Role, &user.IsAdmin, &user.IsActive, &user.CreatedAt, &user.LastLoginAt)
	return &user, err
}

func Logout(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		jti, _ := c.Get("jti")
		if jtiStr, ok := jti.(string); ok && jtiStr != "" {
			db.BlacklistJTI(database, jtiStr)
		}

		refreshToken, _ := c.Cookie(RefreshTokenCookieName)
		if refreshToken == "" {
			var req RefreshRequest
			if err := c.ShouldBindJSON(&req); err == nil {
				refreshToken = req.RefreshToken
			}
		}
		if refreshToken != "" {
			var userID int
			if err := database.QueryRow("SELECT user_id FROM refresh_tokens WHERE token = $1", refreshToken).Scan(&userID); err == nil {
				database.Exec("UPDATE refresh_tokens SET used = true, used_at = NOW() WHERE user_id = $1 AND used = false", userID)
			}
		}
		clearAuthCookies(c)
		c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
	}
}

func GetMe(database *sql.DB) gin.HandlerFunc {
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
		var isAdmin bool
		err := database.QueryRow("SELECT is_admin FROM users WHERE id = $1", userID).Scan(&isAdmin)
		if err != nil {
			isAdmin = false
		}
		exp := int64((*mapClaims)["exp"].(float64))
		c.JSON(http.StatusOK, gin.H{
			"id":                      userID,
			"username":                username,
			"role":                    role,
			"is_admin":                isAdmin,
			"is_active":               true,
			"notifications_enabled":   db.GetSetting(database, "notifications_enabled", "true") == "true",
			"relay_ingestion_enabled": db.GetSetting(database, "relay_ingestion_enabled", "false") == "false",
			"expires_at":              exp,
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

		if err := auth.ValidatePassword(req.NewPassword); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

		audit.LogAudit(database, getUserID(database, username), username, "password_changed", c.ClientIP(), "")
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
