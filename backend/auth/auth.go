package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"logmara/audit"
	"logmara/db"
	"logmara/util"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Config holds everything the auth package needs at runtime.
// Pass it to middleware and handlers instead of relying on globals.
type Config struct {
	jwtSecret []byte
	db        *sql.DB
}

// weakDefaultJWTSecret is the placeholder value that used to ship in
// docker-compose.yml and .env.example. It must never be used to sign real
// tokens, so it is treated as "unset".
const weakDefaultJWTSecret = "change-this-to-a-random-secret-key"

// Init resolves the JWT secret from the environment and returns a ready-to-use
// Config. The secret is never read from or written to the database.
func Init(database *sql.DB) (*Config, error) {
	secret, err := ResolveJWTSecret()
	if err != nil {
		return nil, err
	}
	return &Config{
		jwtSecret: []byte(secret),
		db:        database,
	}, nil
}

// ResolveJWTSecret returns the JWT signing secret from the JWT_SECRET env var
// (or the file JWT_SECRET_FILE points at), or an error explaining how to set
// it. It deliberately never falls back to the database: keeping the signing
// key out of the same store as the data means a database dump alone cannot
// forge session tokens. Shared by Init at startup and the setup wizard's
// pre-flight check so both enforce identical rules.
func ResolveJWTSecret() (string, error) {
	secret := util.SecretFromEnv("JWT_SECRET")
	if secret == "" || secret == weakDefaultJWTSecret {
		return "", fmt.Errorf("JWT_SECRET is not set; generate one (e.g. `openssl rand -base64 48`) and provide it via the JWT_SECRET env var or JWT_SECRET_FILE - see README")
	}
	if len(secret) < 32 {
		return "", fmt.Errorf("JWT_SECRET is too short (%d chars); use at least 32 characters", len(secret))
	}
	return secret, nil
}

func generateJTI() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (cfg *Config) getJWTExpiryMin() int {
	timeoutStr := os.Getenv("SESSION_TIMEOUT_MIN")
	if timeoutStr != "" {
		if t, err := strconv.Atoi(timeoutStr); err == nil && t > 0 {
			return t
		}
	}
	if cfg.db != nil {
		timeoutStr = db.GetSetting(cfg.db, "session_timeout_min", "15")
	}
	if t, err := strconv.Atoi(timeoutStr); err == nil && t > 0 {
		return t
	}
	return 15
}

// GenerateToken creates a signed access JWT. remember mirrors the
// "remember this device" flag of the refresh token this access token was
// issued alongside, so GetMe can tell the frontend whether it's safe to
// silently auto-renew this session instead of prompting the user (see
// AuthProvider.checkSessionExpiry).
func (cfg *Config) GenerateToken(userID int64, username string, role string, remember bool) (string, string, time.Time, error) {
	expiryMin := cfg.getJWTExpiryMin()
	jti := generateJTI()
	exp := time.Now().Add(time.Duration(expiryMin) * time.Minute)
	claims := jwt.MapClaims{
		"user_id":  userID,
		"username": username,
		"role":     role,
		"jti":      jti,
		"exp":      exp.Unix(),
		"iat":      time.Now().Unix(),
		"remember": remember,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString(cfg.jwtSecret)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return tokenStr, jti, exp, nil
}

// ValidateToken parses and validates a JWT string.
func (cfg *Config) ValidateToken(tokenString string) (*jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return cfg.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return &claims, nil
	}
	return nil, fmt.Errorf("invalid token")
}

// RememberedRefreshTokenTTL is how long a refresh token lives when the user
// checked "remember this device" at login, versus the normal 7 days.
const RememberedRefreshTokenTTL = 60 * 24 * time.Hour
const DefaultRefreshTokenTTL = 7 * 24 * time.Hour

// GenerateRefreshToken returns a random refresh token. remember extends its
// expiry from the normal 7 days to 60 days, for "remember this device" logins.
func GenerateRefreshToken(userID int, remember bool) (string, time.Time) {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	ttl := DefaultRefreshTokenTTL
	if remember {
		ttl = RememberedRefreshTokenTTL
	}
	exp := time.Now().Add(ttl)
	return token, exp
}

// HashRefreshToken returns a SHA-256 hex digest of the token for secure storage.
// The raw token is never persisted; only this hash is stored and compared
// during lookup, so a database breach doesn't yield usable session tokens.
func HashRefreshToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// GenerateDeviceID returns a random identifier for a "remember this device"
// cookie, stable across logins/logouts on the same browser.
func GenerateDeviceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// HashPassword hashes a password with bcrypt (cost 14).
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(hash), err
}

var (
	hasUpper   = regexp.MustCompile(`[A-Z]`)
	hasLower   = regexp.MustCompile(`[a-z]`)
	hasDigit   = regexp.MustCompile(`[0-9]`)
	hasSpecial = regexp.MustCompile(`[^A-Za-z0-9]`)
)

// PasswordPolicy holds configurable password requirements loaded from app_settings.
type PasswordPolicy struct {
	MinLength     int
	MaxLength     int
	RequireUpper  bool
	RequireLower  bool
	RequireDigit  bool
	RequireSpecial bool
	HistoryCount  int
}

// LoadPasswordPolicy reads password policy settings from the database.
func LoadPasswordPolicy(getSetting func(string, string) string) PasswordPolicy {
	p := PasswordPolicy{
		MinLength:    envOrInt(getSetting("security_password_min_length", "8"), 8),
		MaxLength:    envOrInt(getSetting("security_password_max_length", "128"), 128),
		RequireUpper: getSetting("security_password_require_upper", "true") == "true",
		RequireLower: getSetting("security_password_require_lower", "true") == "true",
		RequireDigit: getSetting("security_password_require_digit", "true") == "true",
		RequireSpecial: getSetting("security_password_require_special", "true") == "true",
		HistoryCount: envOrInt(getSetting("security_password_history_count", "12"), 12),
	}
	return p
}

func envOrInt(val string, def int) int {
	if v, err := strconv.Atoi(val); err == nil && v > 0 {
		return v
	}
	return def
}

// ValidatePassword enforces complexity requirements (uses defaults, no DB).
func ValidatePassword(password string) error {
	return ValidatePasswordWithPolicy(PasswordPolicy{
		MinLength:    8,
		MaxLength:    128,
		RequireUpper: true,
		RequireLower: true,
		RequireDigit: true,
		RequireSpecial: true,
	}, password)
}

// ValidatePasswordWithPolicy validates a password against the given policy.
func ValidatePasswordWithPolicy(p PasswordPolicy, password string) error {
	if len(password) < p.MinLength {
		return fmt.Errorf("password must be at least %d characters", p.MinLength)
	}
	if len(password) > p.MaxLength {
		return fmt.Errorf("password must not exceed %d characters", p.MaxLength)
	}
	if p.RequireUpper && !hasUpper.MatchString(password) {
		return fmt.Errorf("password must contain at least one uppercase letter")
	}
	if p.RequireLower && !hasLower.MatchString(password) {
		return fmt.Errorf("password must contain at least one lowercase letter")
	}
	if p.RequireDigit && !hasDigit.MatchString(password) {
		return fmt.Errorf("password must contain at least one digit")
	}
	if p.RequireSpecial && !hasSpecial.MatchString(password) {
		return fmt.Errorf("password must contain at least one special character")
	}
	return nil
}

func extractJTI(claims *jwt.MapClaims) string {
	if v, ok := (*claims)["jti"].(string); ok {
		return v
	}
	return ""
}

// JWTRequired returns middleware that validates JWT tokens and checks blacklist.
func (cfg *Config) JWTRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString, _ := c.Cookie("accessToken")
		if tokenString == "" {
			authHeader := c.GetHeader("Authorization")
			if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
				tokenString = authHeader[7:]
			} else if authHeader != "" {
				tokenString = authHeader
			}
		}
		// Deliberately no ?token= query-param fallback: an access token in the
		// URL leaks into nginx access logs, browser history and Referer
		// headers. The SSE stream (the one client that can't set headers with
		// EventSource) is fetched with credentials instead - see
		// frontend streamNotifications - so it rides the cookie like everything else.
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing authorization header", "error_key": "auth.missingAuthorization"})
			c.Abort()
			return
		}

		claims, err := cfg.ValidateToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token", "error_key": "auth.invalidToken"})
			c.Abort()
			return
		}

		jti := extractJTI(claims)
		if jti != "" && cfg.db != nil {
			if blacklisted, err := db.IsJTIBlacklisted(cfg.db, jti); err == nil && blacklisted {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "token revoked", "error_key": "auth.tokenRevoked"})
				c.Abort()
				return
			}
		}

		c.Set("claims", claims)
		c.Set("jti", jti)
		if uid, ok := (*claims)["user_id"].(float64); ok {
			c.Set("user_id", int64(uid))
		}
		c.Next()
	}
}

// RoleRequired returns middleware that enforces at least one of the given roles.
func (cfg *Config) RoleRequired(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get("claims")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required", "error_key": "auth.required"})
			c.Abort()
			return
		}

		mapClaims := claims.(*jwt.MapClaims)
		userRole := (*mapClaims)["role"].(string)
		username, _ := (*mapClaims)["username"].(string)
		userID, _ := (*mapClaims)["user_id"].(float64)

		for _, role := range roles {
			if userRole == role {
				c.Next()
				return
			}
		}

		if cfg.db != nil {
			go func() {
				audit.LogAudit(cfg.db, int64(userID), username, "access_denied", c.ClientIP(), fmt.Sprintf("insufficient permissions; required=%v, user_role=%s", roles, userRole))
			}()
		}

		c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions", "error_key": "auth.insufficientPermissions"})
		c.Abort()
	}
}

// AdminRequired returns middleware that enforces admin role.
func (cfg *Config) AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get("claims")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required", "error_key": "auth.required"})
			c.Abort()
			return
		}

		mapClaims := claims.(*jwt.MapClaims)
		userRole := (*mapClaims)["role"].(string)
		username, _ := (*mapClaims)["username"].(string)
		userID, _ := (*mapClaims)["user_id"].(float64)

		if userRole != "admin" {
			if cfg.db != nil {
				go func() {
					audit.LogAudit(cfg.db, int64(userID), username, "access_denied", c.ClientIP(), fmt.Sprintf("admin access required; user_role=%s", userRole))
				}()
			}

			c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required", "error_key": "auth.adminRequired"})
			c.Abort()
			return
		}

		c.Next()
	}
}