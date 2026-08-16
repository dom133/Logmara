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
func ListSessions(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		uid, ok := extractUserID(c)
		if !ok {
			middleware.HandleError(c, model.NewUnauthorizedKey("auth.required", "Authentication required", nil))
			return
		}

		// The access-token JTI issued alongside the caller's current refresh
		// token is the only thing that identifies *this* session - device_id
		// is a long-lived per-browser cookie shared by every login from that
		// browser (including old, superseded ones), so comparing against it
		// used to mark every session sharing the device as "current".
		var currentJTI string
		if jti, ok := c.Get("jti"); ok {
			currentJTI, _ = jti.(string)
		}

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
			resp = append(resp, sessionResp{Session: s, IsCurrent: currentJTI != "" && s.JTI == currentJTI})
		}
		c.JSON(http.StatusOK, resp)
	}
}

// RevokeSession signs out one of the caller's own sessions by id.
func RevokeSession(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
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
