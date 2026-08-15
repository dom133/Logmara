package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

const (
	DefaultPageLimit   = 100
	MaxPageLimit       = 500
	DefaultLogLimit    = 50
	MaxLogLimit        = 1000
	DefaultExportLimit = 100000
	MaxExportLimit     = 100000
	DefaultParserLimit = 10000
	MaxParserLimit     = 10000
	DefaultAdminLimit  = 100
)

func parseIDParam(id string) (int64, error) {
	return strconv.ParseInt(id, 10, 64)
}

// actorFromContext reads the requesting user's ID and username from the JWT
// claims set by auth.JWTRequired, for use in audit.LogAudit calls.
func actorFromContext(c *gin.Context) (int64, string) {
	userID := c.GetInt64("user_id")
	username := ""
	if claims, exists := c.Get("claims"); exists {
		if mc, ok := claims.(*jwt.MapClaims); ok {
			if u, ok := (*mc)["username"].(string); ok {
				username = u
			}
		}
	}
	return userID, username
}
