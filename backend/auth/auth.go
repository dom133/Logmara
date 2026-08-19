package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync"
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
	secretMu sync.RWMutex
	// HS256 (symmetric) key, always present and used for both signing and
	// validation.
	jwtSecretPrimary   []byte // signing + validation
	jwtSecretSecondary []byte // validation only (grace period during rotation)
	// RS256 (asymmetric) key, optional (dual-mode). When JWT_PRIVATE_KEY is
	// set, new tokens are signed with rsaPriv and validated with rsaPub;
	// existing HS256 tokens keep validating with jwtSecret. rsaPubSecondary is
	// the previous public key kept for the grace period after an RS256 rotation.
	rsaPriv         *rsa.PrivateKey
	rsaPub          *rsa.PublicKey
	rsaPubSecondary *rsa.PublicKey
	pool            *db.DynamicPool
}

// weakDefaultJWTSecret is the placeholder value that used to ship in
// docker-compose.yml and .env.example. It must never be used to sign real
// tokens, so it is treated as "unset".
const weakDefaultJWTSecret = "change-this-to-a-random-secret-key"

// Init resolves the JWT secret from the environment and returns a ready-to-use
// Config. The secret is never read from or written to the database. If
// JWT_PRIVATE_KEY is also set, the RS256 signing key is loaded (dual-mode:
// new tokens are signed with RS256, existing HS256 tokens keep validating with
// JWT_SECRET).
func Init(pool *db.DynamicPool) (*Config, error) {
	secret, err := ResolveJWTSecret()
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		jwtSecretPrimary: []byte(secret),
		pool:             pool,
	}
	if pemKey := util.SecretFromEnv("JWT_PRIVATE_KEY"); pemKey != "" {
		priv, perr := util.ParseRSAPrivateKey(pemKey)
		if perr != nil {
			return nil, fmt.Errorf("JWT_PRIVATE_KEY is set but invalid: %w", perr)
		}
		cfg.rsaPriv = priv
		cfg.rsaPub = priv.Public().(*rsa.PublicKey)
		slog.Info("auth: RS256 signing enabled (JWT_PRIVATE_KEY set); HS256 tokens keep validating via JWT_SECRET")
	}
	return cfg, nil
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
	if cfg.pool != nil {
		timeoutStr = db.GetSetting(cfg.pool.Get(), "session_timeout_min", "15")
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
	cfg.secretMu.RLock()
	rsaPriv := cfg.rsaPriv
	primary := cfg.jwtSecretPrimary
	cfg.secretMu.RUnlock()

	var tokenStr string
	var err error
	if rsaPriv != nil {
		// RS256 (asymmetric): only the private-key holder can mint tokens.
		tokenStr, err = jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(rsaPriv)
	} else {
		// HS256 (symmetric fallback when JWT_PRIVATE_KEY is not configured).
		tokenStr, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(primary)
	}
	if err != nil {
		return "", "", time.Time{}, err
	}
	return tokenStr, jti, exp, nil
}

// ValidateToken parses and validates a JWT string, supporting both signing
// algorithms in dual mode. It dispatches on the token's alg header:
//
//   - RS256 tokens are verified with the RSA public key (JWT_PRIVATE_KEY);
//   - HS256 tokens are verified with JWT_SECRET.
//
// This lets an upgrade from HS256 to RS256 roll out without logging anyone
// out: new tokens are signed with RS256 while in-flight HS256 tokens keep
// validating against JWT_SECRET until they naturally expire. During a key
// rotation the secondary keys (previous RS256 public key / previous HMAC
// secret) are also tried, so tokens minted before the rotation stay valid
// for the grace period.
func (cfg *Config) ValidateToken(tokenString string) (*jwt.MapClaims, error) {
	cfg.secretMu.RLock()
	rsaPub := cfg.rsaPub
	rsaPubSecondary := cfg.rsaPubSecondary
	primary := cfg.jwtSecretPrimary
	secondary := cfg.jwtSecretSecondary
	cfg.secretMu.RUnlock()

	// Primary attempt: current RS256 public key + current HMAC secret.
	if claims, ok := cfg.validateJWT(tokenString, rsaPub, primary); ok {
		return claims, nil
	}
	// Grace-period attempt: previous RS256 public key + previous HMAC secret.
	if rsaPubSecondary != nil || secondary != nil {
		if claims, ok := cfg.validateJWT(tokenString, rsaPubSecondary, secondary); ok {
			return claims, nil
		}
	}
	return nil, fmt.Errorf("invalid token")
}

// validateJWT parses tokenString, verifying an RS256 token with pub (when
// non-nil) or an HS256 token with secret (when non-nil). It reports whether
// the token was accepted.
func (cfg *Config) validateJWT(tokenString string, pub *rsa.PublicKey, secret []byte) (*jwt.MapClaims, bool) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		switch token.Method.(type) {
		case *jwt.SigningMethodRSA:
			if pub == nil {
				return nil, fmt.Errorf("RS256 token but no RSA public key available")
			}
			return pub, nil
		case *jwt.SigningMethodHMAC:
			if secret == nil {
				return nil, fmt.Errorf("HS256 token but no HMAC secret available")
			}
			return secret, nil
		default:
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
	})
	if err == nil {
		if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
			return &claims, true
		}
	}
	return nil, false
}

// RememberedRefreshTokenTTL is the default max lifetime for a "remember this
// device" refresh token. The actual value is loaded from the
// session_remembered_max_days setting at runtime; this constant serves as
// the fallback when the DB is unavailable.
const RememberedRefreshTokenTTL = 60 * 24 * time.Hour
const DefaultRefreshTokenTTL = 7 * 24 * time.Hour

// GenerateRefreshToken returns a random refresh token. remember extends its
// expiry from the normal 7 days to 60 days, for "remember this device" logins.
func GenerateRefreshToken(userID int, remember bool) (string, time.Time) {
	return GenerateRefreshTokenWithTTL(userID, remember, RememberedRefreshTokenTTL)
}

// GenerateRefreshTokenWithTTL is like GenerateRefreshToken but accepts a
// custom TTL for remembered sessions, allowing the caller to read the value
// from the database (session_remembered_max_days setting).
func GenerateRefreshTokenWithTTL(userID int, remember bool, rememberedTTL time.Duration) (string, time.Time) {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	ttl := DefaultRefreshTokenTTL
	if remember {
		ttl = rememberedTTL
	}
	exp := time.Now().Add(ttl)
	return token, exp
}

// HashRefreshToken returns the digest of a refresh token for secure storage.
// It is hex(HMAC-SHA256(TOKEN_HASH_KEY, token)) (see util.TokenHash) - the
// raw token is never persisted, so a database breach doesn't yield usable
// session tokens, and the HMAC keying stops precomputed-digest attacks that
// a plain unsalted SHA-256 would allow.
func HashRefreshToken(token string) string {
	return util.TokenHash(token)
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

// MinPasswordLength is the hard floor for password length. Even if an
// administrator configures a lower security_password_min_length, new passwords
// are never accepted below this, so a weak policy cannot be set by accident.
const MinPasswordLength = 12

// PasswordPolicy holds configurable password requirements loaded from app_settings.
type PasswordPolicy struct {
	MinLength      int
	MaxLength      int
	RequireUpper   bool
	RequireLower   bool
	RequireDigit   bool
	RequireSpecial bool
	HistoryCount   int
}

// LoadPasswordPolicy reads password policy settings from the database.
func LoadPasswordPolicy(getSetting func(string, string) string) PasswordPolicy {
	p := PasswordPolicy{
		MinLength:      envOrInt(getSetting("security_password_min_length", "12"), MinPasswordLength),
		MaxLength:      envOrInt(getSetting("security_password_max_length", "128"), 128),
		RequireUpper:   getSetting("security_password_require_upper", "true") == "true",
		RequireLower:   getSetting("security_password_require_lower", "true") == "true",
		RequireDigit:   getSetting("security_password_require_digit", "true") == "true",
		RequireSpecial: getSetting("security_password_require_special", "true") == "true",
		HistoryCount:   envOrInt(getSetting("security_password_history_count", "12"), 12),
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
		MinLength:      MinPasswordLength,
		MaxLength:      128,
		RequireUpper:   true,
		RequireLower:   true,
		RequireDigit:   true,
		RequireSpecial: true,
	}, password)
}

// ValidatePasswordWithPolicy validates a password against the given policy.
func ValidatePasswordWithPolicy(p PasswordPolicy, password string) error {
	minLen := p.MinLength
	if minLen < MinPasswordLength {
		minLen = MinPasswordLength
	}
	if len(password) < minLen {
		return fmt.Errorf("password must be at least %d characters", minLen)
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
			if authHeader != "" {
				// Strict scheme: the Authorization header must use "Bearer". A
				// present-but-malformed header is a client error (400),
				// distinguished from a missing/invalid token (401).
				if len(authHeader) < 7 || authHeader[:7] != "Bearer " {
					c.JSON(http.StatusBadRequest, gin.H{"error": "Authorization header must use the Bearer scheme", "error_key": "auth.invalidAuthFormat"})
					c.Abort()
					return
				}
				tokenString = authHeader[7:]
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
		if jti != "" && cfg.pool != nil {
			if blacklisted, err := db.IsJTIBlacklisted(cfg.pool.Get(), jti); err == nil && blacklisted {
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

// RotateSecret atomically rotates the JWT signing key. The current primary
// key becomes the secondary key (used for validation during the grace
// period), and the new key becomes the primary key (used for signing and
// validation). Existing tokens signed with the old key remain valid until
// they naturally expire (SESSION_TIMEOUT_MIN).
func (cfg *Config) RotateSecret(newSecret string) {
	cfg.secretMu.Lock()
	cfg.jwtSecretSecondary = cfg.jwtSecretPrimary
	cfg.jwtSecretPrimary = []byte(newSecret)
	cfg.secretMu.Unlock()
	slog.Info("auth: JWT secret rotated, secondary key set for grace period")
}

// ClearSecondarySecret removes the secondary key after the grace period
// has elapsed. Tokens signed with the old key will be rejected after this.
func (cfg *Config) ClearSecondarySecret() {
	cfg.secretMu.Lock()
	cfg.jwtSecretSecondary = nil
	cfg.secretMu.Unlock()
	slog.Info("auth: JWT secondary key cleared")
}

// RotateRSAKey atomically rotates the RS256 signing key (dual-mode). The
// previous public key becomes the secondary (used for validating RS256 tokens
// minted before the rotation during the grace period), and the new key becomes
// primary for both signing and validation.
func (cfg *Config) RotateRSAKey(privateKey *rsa.PrivateKey) {
	cfg.secretMu.Lock()
	if cfg.rsaPub != nil {
		cfg.rsaPubSecondary = cfg.rsaPub
	}
	cfg.rsaPriv = privateKey
	cfg.rsaPub = privateKey.Public().(*rsa.PublicKey)
	cfg.secretMu.Unlock()
	slog.Info("auth: RS256 key rotated, secondary public key set for grace period")
}

// ClearSecondaryRSAKey removes the secondary RS256 public key after the grace
// period has elapsed. RS256 tokens signed with the old private key are
// rejected after this.
func (cfg *Config) ClearSecondaryRSAKey() {
	cfg.secretMu.Lock()
	cfg.rsaPubSecondary = nil
	cfg.secretMu.Unlock()
	slog.Info("auth: RS256 secondary public key cleared")
}

// HasSecondarySecret returns true if a secondary (grace period) key is active.
func (cfg *Config) HasSecondarySecret() bool {
	cfg.secretMu.RLock()
	defer cfg.secretMu.RUnlock()
	return len(cfg.jwtSecretSecondary) > 0
}

// HasRSASigning reports whether RS256 signing is active (JWT_PRIVATE_KEY set).
func (cfg *Config) HasRSASigning() bool {
	cfg.secretMu.RLock()
	defer cfg.secretMu.RUnlock()
	return cfg.rsaPriv != nil
}

// GetPrimarySecret returns the current primary JWT secret as a string.
func (cfg *Config) GetPrimarySecret() string {
	cfg.secretMu.RLock()
	defer cfg.secretMu.RUnlock()
	return string(cfg.jwtSecretPrimary)
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

		if cfg.pool != nil {
			go func() {
				audit.LogAudit(cfg.pool.Get(), int64(userID), username, "access_denied", c.ClientIP(), fmt.Sprintf("insufficient permissions; required=%v, user_role=%s", roles, userRole))
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
			if cfg.pool != nil {
				go func() {
					audit.LogAudit(cfg.pool.Get(), int64(userID), username, "access_denied", c.ClientIP(), fmt.Sprintf("admin access required; user_role=%s", userRole))
				}()
			}

			c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required", "error_key": "auth.adminRequired"})
			c.Abort()
			return
		}

		c.Next()
	}
}
