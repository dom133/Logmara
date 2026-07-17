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

	"syslog-gui/db"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var jwtSecret []byte
var jwtExpiryMin = 15

func Init(database *sql.DB) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = db.GetSetting(database, "jwt_secret", "")
	}
	if secret == "" {
		secret = generateRandomKey()
		db.UpdateSetting(database, "jwt_secret", secret)
	}
	jwtSecret = []byte(secret)

	timeoutStr := os.Getenv("SESSION_TIMEOUT_MIN")
	if timeoutStr == "" {
		timeoutStr = db.GetSetting(database, "session_timeout_min", "15")
	}
	if t, err := strconv.Atoi(timeoutStr); err == nil && t > 0 {
		jwtExpiryMin = t
	}
}

func generateRandomKey() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func GenerateToken(userID int64, username string, role string) (string, error) {
	claims := jwt.MapClaims{
		"user_id":  userID,
		"username": username,
		"role":     role,
		"exp":      time.Now().Add(time.Duration(jwtExpiryMin) * time.Minute).Unix(),
		"iat":      time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

func ValidateToken(tokenString string) (*jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return &claims, nil
	}
	return nil, fmt.Errorf("invalid token")
}

func GenerateRefreshToken(userID int) (string, time.Time) {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	exp := time.Now().Add(7 * 24 * time.Hour)
	return token, exp
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(hash), err
}

func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

var (
	hasUpper  = regexp.MustCompile(`[A-Z]`)
	hasLower  = regexp.MustCompile(`[a-z]`)
	hasDigit  = regexp.MustCompile(`[0-9]`)
	hasSpecial = regexp.MustCompile(`[^A-Za-z0-9]`)
)

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

func JWTRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		tokenString := ""
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			tokenString = authHeader[7:]
		} else if authHeader != "" {
			tokenString = authHeader
		}
		if tokenString == "" {
			tokenString = c.Query("token")
		}
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing authorization header"})
			c.Abort()
			return
		}

		claims, err := ValidateToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		c.Set("claims", claims)
		if uid, ok := (*claims)["user_id"].(float64); ok {
			c.Set("user_id", int64(uid))
		}
		c.Next()
	}
}

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
