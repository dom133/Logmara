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

var validRoles = []string{RoleAdmin, RoleEditor, RoleViewer}

// isValidRole returns true if role is one of admin/editor/viewer.
func isValidRole(role string) bool {
	for _, r := range validRoles {
		if role == r {
			return true
		}
	}
	return false
}

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

// extractUserID extracts the user_id (int64) from gin.Context. Returns (0, false) if unavailable.
func extractUserID(c *gin.Context) (int64, bool) {
	if uid, ok := c.Get("user_id"); ok {
		if id, ok := uid.(int64); ok {
			return id, true
		}
	}
	return 0, false
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
