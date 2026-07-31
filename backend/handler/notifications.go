package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"logmara/db"
	"logmara/middleware"
	"logmara/model"
	"logmara/notify"
	"logmara/notifyhub"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// RequireNotificationsEnabled blocks the alert/channel/history management
// API (not the notification bell's own GET /notifications, which needs to
// stay reachable so it can report enabled:false and hide itself) whenever
// the notifications_enabled setting is off.
func RequireNotificationsEnabled(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if db.GetSetting(database, "notifications_enabled", "true") != "true" {
			middleware.HandleError(c, model.NewForbiddenKey("notifications.disabled", "Notifications are disabled", nil))
			return
		}
		c.Next()
	}
}

func ListNotificationChannels(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		channels, err := db.GetAllNotificationChannels(database)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("notifications.channelsListFailed", "Failed to list notification channels", err))
			return
		}
		c.JSON(http.StatusOK, channels)
	}
}

func CreateNotificationChannel(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.NotificationChannelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequestBody", "Invalid request body", err))
			return
		}

		var createdBy int64
		if id, ok := extractUserID(c); ok {
			createdBy = id
		}

		channel, err := db.CreateNotificationChannel(database, req, createdBy)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("notifications.channelCreateFailed", "Failed to create notification channel", err))
			return
		}
		c.JSON(http.StatusCreated, channel)
	}
}

// channelOwnedByCaller loads the channel and checks that the caller may
// modify it: either they created it, or - for a channel with no recorded
// owner (seeded before the created_by column existed, so there's no real
// creator to compare against) - they're an admin. That fallback is
// deliberately admin-only, not "any editor": an unclaimed legacy channel
// isn't fair game for whichever editor happens to click Edit first, only
// for the role that could already manage every channel before per-channel
// ownership existed. Returns nil and writes an error response if the check
// fails.
func channelOwnedByCaller(c *gin.Context, database *sql.DB, id int64) (*model.NotificationChannel, bool) {
	channel, err := db.GetNotificationChannel(database, id)
	if err != nil {
		if err == sql.ErrNoRows {
			middleware.HandleError(c, model.NewNotFoundKey("notifications.channelNotFound", "Notification channel not found", nil))
			return nil, false
		}
		middleware.HandleError(c, model.NewInternalKey("notifications.channelLoadFailed", "Failed to load notification channel", err))
		return nil, false
	}

	if !callerCanModifyChannel(c, channel) {
		if channel.CreatedBy != nil {
			middleware.HandleError(c, model.NewForbiddenKey("notifications.modifyOwnOnly", "You can only modify notification channels you created", nil))
		} else {
			middleware.HandleError(c, model.NewForbiddenKey("notifications.adminOnlyModify", "Only an admin can modify a channel with no recorded owner", nil))
		}
		return nil, false
	}
	return channel, true
}

// callerCanModifyChannel decides, without touching the DB, whether the
// caller may modify channel: they created it, or - for a channel with no
// recorded owner (seeded before the created_by column existed, so there's
// no real creator to compare against) - they're an admin. Split out from
// channelOwnedByCaller so this decision can be unit tested against a plain
// gin.Context without a real database connection.
func callerCanModifyChannel(c *gin.Context, channel *model.NotificationChannel) bool {
	if channel.CreatedBy != nil {
		callerID, ok := extractUserID(c)
		return ok && *channel.CreatedBy == callerID
	}

	claims, exists := c.Get("claims")
	mapClaims, okClaims := claims.(*jwt.MapClaims)
	if !exists || !okClaims {
		return false
	}
	role, _ := (*mapClaims)["role"].(string)
	return role == "admin"
}

func UpdateNotificationChannel(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidId", "invalid id", nil))
			return
		}

		if _, ok := channelOwnedByCaller(c, database, id); !ok {
			return
		}

		var req model.NotificationChannelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequestBody", "Invalid request body", err))
			return
		}

		channel, err := db.UpdateNotificationChannel(database, id, req)
		if err != nil {
			if err == sql.ErrNoRows {
				middleware.HandleError(c, model.NewNotFoundKey("notifications.channelNotFound", "Notification channel not found", nil))
				return
			}
			middleware.HandleError(c, model.NewInternalKey("notifications.channelUpdateFailed", "Failed to update notification channel", err))
			return
		}
		c.JSON(http.StatusOK, channel)
	}
}

func DeleteNotificationChannel(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidId", "invalid id", nil))
			return
		}

		if _, ok := channelOwnedByCaller(c, database, id); !ok {
			return
		}

		if err := db.DeleteNotificationChannel(database, id); err != nil {
			middleware.HandleError(c, model.NewInternalKey("notifications.channelDeleteFailed", "Failed to delete notification channel", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "notification channel deleted"})
	}
}

func TestNotificationChannel(database *sql.DB, hub *notifyhub.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidId", "invalid id", nil))
			return
		}

		channel, err := db.GetNotificationChannel(database, id)
		if err != nil {
			if err == sql.ErrNoRows {
				middleware.HandleError(c, model.NewNotFoundKey("notifications.channelNotFound", "Notification channel not found", nil))
				return
			}
		middleware.HandleError(c, model.NewInternalKey("notifications.channelLoadFailed", "Failed to load notification channel", err))
			return
		}

		if err := notify.TestChannel(database, *channel, hub.Publish); err != nil {
			middleware.HandleError(c, model.NewInternalKey("notifications.testFailed", "Test notification failed", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "test notification sent"})
	}
}

type NotificationHistoryRequest struct {
	Limit int `json:"limit"`
}

func GetNotificationHistory(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req NotificationHistoryRequest
		_ = c.ShouldBindJSON(&req)
		limit := req.Limit
		if limit <= 0 {
			limit = DefaultAdminLimit
		}

		userID := c.GetInt64("user_id")
		isAdmin := db.IsUserAdmin(database, userID)
		entries, err := db.GetNotificationHistory(database, limit, isAdmin, userID)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("notifications.historyLoadFailed", "Failed to load notification history", err))
			return
		}
		c.JSON(http.StatusOK, entries)
	}
}

func ClearNotificationHistory(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := db.ClearNotificationHistory(database); err != nil {
			middleware.HandleError(c, model.NewInternalKey("notifications.historyClearFailed", "Failed to clear notification history", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "notification history cleared"})
	}
}

// GetNotifications returns the signed-in user's unread count plus their
// still-unread in-app notifications, for the bell dropdown's initial load
// (the live stream in StreamNotifications handles updates after that).
// Once something is marked read - whether via the "Clear all" button or
// just by opening the bell - it drops out of this list for good, so a
// "cleared" bell stays cleared across a page reload instead of the same
// items reappearing every time.
func GetNotifications(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetInt64("user_id")
		isAdmin := db.IsUserAdmin(database, userID)

		lastRead, err := db.GetLastReadID(database, userID)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("notifications.loadFailed", "Failed to load notifications", err))
			return
		}
		count, lastID, err := db.GetUnreadNotificationCount(database, userID, isAdmin)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("notifications.loadFailed", "Failed to load notifications", err))
			return
		}
		items, err := db.GetInAppNotifications(database, lastRead, 20, isAdmin, userID)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("notifications.loadFailed", "Failed to load notifications", err))
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"enabled":       db.GetSetting(database, "notifications_enabled", "true") == "true",
			"unread_count":  count,
			"last_id":       lastID,
			"notifications": items,
		})
	}
}

type MarkNotificationsReadRequest struct {
	LastReadID int64 `json:"last_read_id" binding:"required"`
}

func MarkNotificationsRead(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetInt64("user_id")

		var req MarkNotificationsReadRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequestBody", "Invalid request body", err))
			return
		}

		if err := db.MarkNotificationsRead(database, userID, req.LastReadID); err != nil {
			middleware.HandleError(c, model.NewInternalKey("notifications.markReadFailed", "Failed to mark notifications read", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "marked read"})
	}
}

// StreamNotifications is a Server-Sent Events endpoint: it holds the
// connection open and pushes each new in-app notification as it's
// published, via notifyhub.Hub (which itself fans out over Redis pub/sub
// when running with multiple api replicas).
func StreamNotifications(hub *notifyhub.Hub, database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			middleware.HandleError(c, model.NewInternalKey("notifications.streamingUnsupported", "Streaming unsupported", nil))
			return
		}

		// The server's WriteTimeout (15s, see main.go) exists to bound normal
		// request/response cycles and would otherwise kill this connection
		// out from under us shortly after it opens, well before the client
		// ever sees a second event - nginx logs that as "upstream
		// prematurely closed connection". Disable it for just this
		// connection; every other route keeps the 15s limit.
		_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})

		userID := c.GetInt64("user_id")
		isAdmin := db.IsUserAdmin(database, userID)

		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")
		c.Writer.WriteHeader(http.StatusOK)
		flusher.Flush()

		ch := hub.Subscribe()
		defer hub.Unsubscribe(ch)

		keepalive := time.NewTicker(20 * time.Second)
		defer keepalive.Stop()

		for {
			select {
			case <-c.Request.Context().Done():
				return
			case n := <-ch:
				targeted := false
				if len(n.TargetUserIds) > 0 {
					for _, uid := range n.TargetUserIds {
						if uid == userID {
							targeted = true
							break
						}
					}
					if !targeted {
						continue
					}
				}
				// Broadcast (no specific targets) admin-only notifications
				// stay hidden from non-admins; being specifically targeted
				// overrides that, same reasoning as db.GetInAppNotifications.
				if !targeted && !isAdmin && model.IsAdminOnlyRuleType(n.AlertRuleType) {
					continue
				}
				b, err := json.Marshal(n)
				if err != nil {
					continue
				}
				fmt.Fprintf(c.Writer, "data: %s\n\n", b)
				flusher.Flush()
			case <-keepalive.C:
				fmt.Fprint(c.Writer, ": keepalive\n\n")
				flusher.Flush()
			}
		}
	}
}
