package handler

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
)

func CSRFRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			_, err := c.Cookie(CSRFTokenCookieName)
			if err != nil {
				token := generateCSRFToken()
				secure := isHTTPS(c)
				http.SetCookie(c.Writer, &http.Cookie{
					Name:     CSRFTokenCookieName,
					Value:    token,
					Path:     "/",
					HttpOnly: false,
					Secure:   secure,
					SameSite: http.SameSiteStrictMode,
					MaxAge:   86400,
				})
			}
			c.Next()
			return
		}

		headerToken := c.GetHeader("X-CSRF-Token")
		cookieToken, err := c.Cookie(CSRFTokenCookieName)

		if err != nil || cookieToken == "" || headerToken == "" || headerToken != cookieToken {
			c.JSON(http.StatusForbidden, gin.H{"error": "CSRF token mismatch"})
			c.Abort()
			return
		}

		c.Next()
	}
}

func generateCSRFToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
