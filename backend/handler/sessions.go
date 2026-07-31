package handler

import (
	"database/sql"
	"net/http"

	"logmara/db"
	"logmara/middleware"
	"logmara/model"

	"github.com/gin-gonic/gin"
)

// ListSessions returns the caller's own active sessions (devices/browsers
// with a still-usable refresh token) so they can review and sign out of any
// they don't recognize.
func ListSessions(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := extractUserID(c)
		if !ok {
			middleware.HandleError(c, model.NewUnauthorizedKey("auth.required", "Authentication required", nil))
			return
		}

		currentDeviceID, _ := c.Cookie(DeviceIDCookieName)

		sessions, err := db.ListUserSessions(database, uid)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("sessions.listFailed", "Failed to list sessions", err))
			return
		}

		type sessionResp struct {
			db.Session
			IsCurrent bool `json:"is_current"`
		}
		resp := make([]sessionResp, 0, len(sessions))
		for _, s := range sessions {
			resp = append(resp, sessionResp{Session: s, IsCurrent: currentDeviceID != "" && s.DeviceID == currentDeviceID})
		}
		c.JSON(http.StatusOK, resp)
	}
}

// RevokeSession signs out one of the caller's own sessions by id.
func RevokeSession(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := extractUserID(c)
		if !ok {
			middleware.HandleError(c, model.NewUnauthorizedKey("auth.required", "Authentication required", nil))
			return
		}

		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("sessions.invalidId", "invalid id", nil))
			return
		}

		if err := db.RevokeSession(database, uid, id); err != nil {
			if err == sql.ErrNoRows {
				middleware.HandleError(c, model.NewNotFoundKey("sessions.notFound", "Session not found", nil))
				return
			}
			middleware.HandleError(c, model.NewInternalKey("sessions.revokeFailed", "Failed to revoke session", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "session revoked"})
	}
}
