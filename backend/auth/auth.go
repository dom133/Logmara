package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"syslytics/db"

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

// weakDefaultJWTSecret is the placeholder value shipped in docker-compose.yml
// and .env.example. It must never be used to sign real tokens.
const weakDefaultJWTSecret = "change-this-to-a-random-secret-key"

// Init validates the JWT secret, persists a generated one if missing,
// and returns a ready-to-use Config.
func Init(database *sql.DB) (*Config, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == weakDefaultJWTSecret {
		secret = ""
	} else if secret != "" && len(secret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET is too short (%d chars); use at least 32 characters", len(secret))
	}
	if secret == "" {
		secret = db.GetSetting(database, "jwt_secret", "")
	}
	if secret != "" && len(secret) < 32 {
		return nil, fmt.Errorf("persisted jwt_secret is too short (%d chars); use at least 32 characters", len(secret))
	}
	if secret == "" {
		secret = generateRandomKey()
		db.UpdateSetting(database, "jwt_secret", secret)
	}
	return &Config{
		jwtSecret: []byte(secret),
		db:        database,
	}, nil
}

func generateRandomKey() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
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

// GenerateToken creates a signed access JWT.
func (cfg *Config) GenerateToken(userID int64, username string, role string) (string, string, time.Time, error) {
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

// GenerateRefreshToken returns a random refresh token with 7-day expiry.
func GenerateRefreshToken(userID int) (string, time.Time) {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	exp := time.Now().Add(7 * 24 * time.Hour)
	return token, exp
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

// ValidatePassword enforces complexity requirements.
func ValidatePassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	if len(password) > 128 {
		return fmt.Errorf("password must not exceed 128 characters")
	}
	if !hasUpper.MatchString(password) {
		return fmt.Errorf("password must contain at least one uppercase letter")
	}
	if !hasLower.MatchString(password) {
		return fmt.Errorf("password must contain at least one lowercase letter")
	}
	if !hasDigit.MatchString(password) {
		return fmt.Errorf("password must contain at least one digit")
	}
	if !hasSpecial.MatchString(password) {
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
		if tokenString == "" {
			tokenString = c.Query("token")
		}
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing authorization header"})
			c.Abort()
			return
		}

		claims, err := cfg.ValidateToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		jti := extractJTI(claims)
		if jti != "" && cfg.db != nil {
			if blacklisted, err := db.IsJTIBlacklisted(cfg.db, jti); err == nil && blacklisted {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "token revoked"})
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
func RoleRequired(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get("claims")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			c.Abort()
			return
		}

		mapClaims := claims.(*jwt.MapClaims)
		userRole := (*mapClaims)["role"].(string)

		for _, role := range roles {
			if userRole == role {
				c.Next()
				return
			}
		}

		c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
		c.Abort()
	}
}

// AdminRequired returns middleware that enforces admin role.
func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get("claims")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			c.Abort()
			return
		}

		mapClaims := claims.(*jwt.MapClaims)
		userRole := (*mapClaims)["role"].(string)

		if userRole != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
			c.Abort()
			return
		}

		c.Next()
	}
}