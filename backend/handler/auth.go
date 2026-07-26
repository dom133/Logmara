package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"logmara/audit"
	"logmara/auth"
	"logmara/db"
	"logmara/ldap"
	"logmara/middleware"
	"logmara/model"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	AccessTokenCookieName  = "accessToken"
	RefreshTokenCookieName = "refreshToken"
	CSRFTokenCookieName    = "csrf_token"
	DeviceIDCookieName     = "device_id"
	deviceIDCookieMaxAge   = 400 * 24 * 60 * 60 // ~400 days, the practical cap browsers enforce on cookie lifetime
)

// deviceID returns the long-lived per-browser identifier used to group
// "remember this device" refresh tokens into sessions a user can review and
// revoke individually. It reads the existing device_id cookie, or mints and
// sets a new one if this is the browser's first login.
func deviceID(c *gin.Context) string {
	if id, err := c.Cookie(DeviceIDCookieName); err == nil && id != "" {
		return id
	}
	id := auth.GenerateDeviceID()
	secure := c.GetHeader("X-Forwarded-Proto") == "https"
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     DeviceIDCookieName,
		Value:    id,
		Path:     "/",
		MaxAge:   deviceIDCookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	return id
}

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
	Remember bool   `json:"remember"`
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

func Login(database *sql.DB, authCfg *auth.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
			return
		}

		var user *db.User

		existing, err := db.GetUserByUsername(database, req.Username)
		if err == nil {
			if locked, _ := db.CheckUserLockout(database, existing.ID); locked {
				audit.LogAudit(database, existing.ID, req.Username, "login_failed_lockout", c.ClientIP(), "account locked due to too many failed attempts")
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "Account temporarily locked due to too many failed login attempts. Try again later."})
				return
			}
			if !existing.IsActive {
				dummyHash := "$2b$14$AAAAAAAAAAAAAAAAAAAAAAAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(req.Password))
				audit.LogAudit(database, existing.ID, req.Username, "login_failed_inactive", c.ClientIP(), "account deactivated by admin")
				c.JSON(http.StatusForbidden, gin.H{"error": "Your account has been deactivated. Contact your administrator."})
				return
			}
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
				if err := db.IncrementFailedLogins(database, existing.ID); err != nil {
					slog.Error("failed to increment failed logins", "error", err, "user_id", existing.ID)
				}
			}
				audit.LogAudit(database, 0, req.Username, "login_failed", c.ClientIP(), "invalid user or inactive")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
				return
			}
		}

		db.ResetFailedLogins(database, user.ID)
		db.UpdateLastLogin(database, user.Username)
		user.LastLoginAt = ptrTime(time.Now())

		token, jti, accessExpiresAt, err := authCfg.GenerateToken(user.ID, user.Username, user.Role, req.Remember)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
			return
		}

		refreshToken, refreshExpiresAt := auth.GenerateRefreshToken(int(user.ID), req.Remember)
		devID := deviceID(c)
		if err := insertRefreshToken(database, refreshTokenParams{
			userID:    int(user.ID),
			token:     refreshToken,
			expiresAt: refreshExpiresAt,
			deviceID:  devID,
			userAgent: c.Request.UserAgent(),
			ip:        c.ClientIP(),
			remember:  req.Remember,
			jti:       jti,
		}); err != nil {
			slog.Error("failed to store refresh token", "error", err)
		}

		audit.LogAudit(database, user.ID, user.Username, "login_success", c.ClientIP(), "")

		go db.RefreshMV(database)

		setAuthCookies(c, token, refreshToken, accessExpiresAt, refreshExpiresAt)

		userResp := gin.H{
			"id":                      user.ID,
			"username":                user.Username,
			"email":                   user.Email,
			"role":                    user.Role,
			"auth_type":               user.AuthType,
			"is_admin":                user.IsAdmin,
			"is_active":               user.IsActive,
			"created_at":              user.CreatedAt,
			"last_login_at":           user.LastLoginAt,
			"notifications_enabled":   db.GetSetting(database, "notifications_enabled", "true") == "true",
			"relay_ingestion_enabled": db.GetSetting(database, "relay_ingestion_enabled", "false") == "true",
		}

		c.JSON(http.StatusOK, gin.H{
			"user":       userResp,
			"expires_at": accessExpiresAt.Unix(),
			"remembered": req.Remember,
		})
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

// refreshReuseGraceWindow tolerates a refresh token being presented twice in
// quick succession (e.g. two open tabs racing to refresh the same stored
// token). The first request rotates the token atomically; a second request
// arriving within this window is handed the token that already won the race
// instead of being logged out. Reuse outside this window is treated as a
// real replay and rejected.
const refreshReuseGraceWindow = 10 * time.Second

func Refresh(database *sql.DB, authCfg *auth.Config) gin.HandlerFunc {
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

		// The frontend sends this on its silent, boot-time restore attempt
		// (AuthProvider.loadUser after the access token has already expired)
		// but never on the user-initiated "Extend Session" click, which must
		// keep working regardless of remember. A session that wasn't set up
		// with "remember this device" is only meant to survive as long as its
		// access token or an active tab keeps extending it - not come back to
		// life on its own after the browser was reopened - so silently reject
		// without consuming the token, leaving it available for that Extend
		// Session click if the tab is in fact still open.
		if c.GetHeader("X-Silent-Refresh") == "true" {
			var remembered bool
			err := database.QueryRow(
				`SELECT remember FROM refresh_tokens WHERE token = $1 AND used = false AND expires_at > NOW()`,
				refreshToken,
			).Scan(&remembered)
			if err != nil || !remembered {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "silent refresh not allowed for this session"})
				return
			}
		}

		var userID int
		var deviceIDVal sql.NullString
		var remember bool
		claimErr := database.QueryRow(
			`UPDATE refresh_tokens SET used = true, used_at = NOW()
			 WHERE token = $1 AND used = false AND expires_at > NOW()
			 RETURNING user_id, device_id, remember`,
			refreshToken,
		).Scan(&userID, &deviceIDVal, &remember)

		var replacementToken string
		var replacementExpiry time.Time
		if claimErr != nil {
			recoveredUserID, recoveredToken, recoveredExpiry, recoveredRemember, recovered := recoverRacedRefresh(database, refreshToken)
			if !recovered {
				clearAuthCookies(c)
				audit.LogAudit(database, 0, "", "refresh_failed", c.ClientIP(), "invalid, expired, or reused refresh token")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired refresh token"})
				return
			}
			userID = recoveredUserID
			replacementToken = recoveredToken
			replacementExpiry = recoveredExpiry
			remember = recoveredRemember
		}

		user, err := getUserByID(database, userID)
		if err != nil {
			clearAuthCookies(c)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
			return
		}

		token, newJTI, accessExpiresAt, err := authCfg.GenerateToken(user.ID, user.Username, user.Role, remember)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
			return
		}

		if replacementToken != "" {
			audit.LogAudit(database, user.ID, user.Username, "refresh_race_recovered", c.ClientIP(), "")
			setAuthCookies(c, token, replacementToken, accessExpiresAt, replacementExpiry)
			c.JSON(http.StatusOK, gin.H{"success": true, "expires_at": accessExpiresAt.Unix(), "remembered": remember})
			return
		}

		newRefreshToken, newExpiresAt := auth.GenerateRefreshToken(userID, remember)
		if err := insertRefreshToken(database, refreshTokenParams{
			userID:    userID,
			token:     newRefreshToken,
			expiresAt: newExpiresAt,
			deviceID:  deviceIDVal.String,
			userAgent: c.Request.UserAgent(),
			ip:        c.ClientIP(),
			remember:  remember,
			jti:       newJTI,
		}); err != nil {
			slog.Error("failed to store refresh token", "error", err)
		}
		if _, err := database.Exec("UPDATE refresh_tokens SET replaced_by = $1 WHERE token = $2", newRefreshToken, refreshToken); err != nil {
			slog.Error("failed to link rotated refresh token", "error", err)
		}

		audit.LogAudit(database, user.ID, user.Username, "refresh_success", c.ClientIP(), "")

		setAuthCookies(c, token, newRefreshToken, accessExpiresAt, newExpiresAt)
		c.JSON(http.StatusOK, gin.H{"success": true, "expires_at": accessExpiresAt.Unix(), "remembered": remember})
	}
}

// recoverRacedRefresh checks whether the presented token was already
// rotated moments ago (used=true, linked to a replacement) and, if so,
// within the grace window, hands back the replacement token instead of
// treating this as a stale/replayed refresh token.
func recoverRacedRefresh(database *sql.DB, token string) (userID int, replacementToken string, replacementExpiry time.Time, remember bool, ok bool) {
	var replacedBy sql.NullString
	var usedAt sql.NullTime
	err := database.QueryRow(
		"SELECT user_id, used_at, replaced_by FROM refresh_tokens WHERE token = $1 AND used = true",
		token,
	).Scan(&userID, &usedAt, &replacedBy)
	if err != nil || !replacedBy.Valid || !usedAt.Valid {
		return 0, "", time.Time{}, false, false
	}
	if time.Since(usedAt.Time) > refreshReuseGraceWindow {
		return 0, "", time.Time{}, false, false
	}

	var expiresAt time.Time
	err = database.QueryRow(
		"SELECT expires_at, remember FROM refresh_tokens WHERE token = $1 AND expires_at > NOW()",
		replacedBy.String,
	).Scan(&expiresAt, &remember)
	if err != nil {
		return 0, "", time.Time{}, false, false
	}

	return userID, replacedBy.String, expiresAt, remember, true
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
			// Scoped to this exact token (one row, one device/session) - not
			// "every refresh token this user has", which would log out every
			// other device/browser too, including any "remember this device"
			// session that's supposed to survive independently of this one.
			database.Exec("UPDATE refresh_tokens SET used = true, used_at = NOW() WHERE token = $1 AND used = false", refreshToken)
		}
		clearAuthCookies(c)
		c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
	}
}

// CheckSession is a lightweight endpoint for the frontend to poll
// periodically after login (see auth.tsx). It does no work of its own -
// JWTRequired (which every authGroup route, including this one, already
// runs) rejects the request with 401 the moment the access token's JTI is
// blacklisted, which is exactly what RevokeSession/Logout now do for the
// token they invalidate. Reaching this handler at all means the session is
// still good; a 401 here means it's been signed out from elsewhere (Admin,
// another device's "Sign out" in My Sessions, or this same session's
// Logout), and the frontend's axios response interceptor already redirects
// to /login on any 401 - so there's nothing else for this handler to do.
func CheckSession(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"active": true})
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
		remembered, _ := (*mapClaims)["remember"].(bool)
		c.JSON(http.StatusOK, gin.H{
			"id":                      userID,
			"username":                username,
			"role":                    role,
			"is_admin":                isAdmin,
			"is_active":               true,
			"notifications_enabled":   db.GetSetting(database, "notifications_enabled", "true") == "true",
			"relay_ingestion_enabled": db.GetSetting(database, "relay_ingestion_enabled", "false") == "true",
			"expires_at":              exp,
			"remembered":              remembered,
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
			middleware.HandleError(c, model.NewBadRequest("Password does not meet requirements", err))
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

type refreshTokenParams struct {
	userID    int
	token     string
	expiresAt time.Time
	deviceID  string
	userAgent string
	ip        string
	remember  bool
	jti       string
}

func insertRefreshToken(db *sql.DB, p refreshTokenParams) error {
	_, err := db.Exec(
		`INSERT INTO refresh_tokens (user_id, token, expires_at, used, device_id, user_agent, ip, remember, jti, last_used_at)
		 VALUES ($1, $2, $3, false, $4, $5, $6, $7, $8, NOW())`,
		p.userID, p.token, p.expiresAt, p.deviceID, p.userAgent, p.ip, p.remember, p.jti,
	)
	return err
}

func getUserID(db *sql.DB, username string) int64 {
	var id int64
	db.QueryRow("SELECT id FROM users WHERE username = $1", username).Scan(&id)
	return id
}