package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"syslog-gui/db"
	"syslog-gui/middleware"
	"syslog-gui/model"
	"syslog-gui/notify"
	"syslog-gui/notifyhub"

	"github.com/gin-gonic/gin"
)

func ListNotificationChannels(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		channels, err := db.GetAllNotificationChannels(database)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to list notification channels", err))
			return
		}
		c.JSON(http.StatusOK, channels)
	}
}

func CreateNotificationChannel(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.NotificationChannelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Invalid request body", err))
			return
		}

		channel, err := db.CreateNotificationChannel(database, req)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to create notification channel", err))
			return
		}
		c.JSON(http.StatusCreated, channel)
	}
}

func UpdateNotificationChannel(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		var req model.NotificationChannelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Invalid request body", err))
			return
		}

		channel, err := db.UpdateNotificationChannel(database, id, req)
		if err != nil {
			if err == sql.ErrNoRows {
				middleware.HandleError(c, model.NewNotFound("Notification channel not found", nil))
				return
			}
			middleware.HandleError(c, model.NewInternal("Failed to update notification channel", err))
			return
		}
		c.JSON(http.StatusOK, channel)
	}
}

func DeleteNotificationChannel(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		if err := db.DeleteNotificationChannel(database, id); err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to delete notification channel", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "notification channel deleted"})
	}
}

func TestNotificationChannel(database *sql.DB, hub *notifyhub.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		channel, err := db.GetNotificationChannel(database, id)
		if err != nil {
			if err == sql.ErrNoRows {
				middleware.HandleError(c, model.NewNotFound("Notification channel not found", nil))
				return
			}
			middleware.HandleError(c, model.NewInternal("Failed to load notification channel", err))
			return
		}

		if err := notify.TestChannel(database, *channel, hub.Publish); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Test notification failed: "+err.Error(), err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "test notification sent"})
	}
}

func GetNotificationHistory(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := DefaultAdminLimit
		if v := c.Query("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}

		entries, err := db.GetNotificationHistory(database, limit)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to load notification history", err))
			return
		}
		c.JSON(http.StatusOK, entries)
	}
}

func ClearNotificationHistory(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := db.ClearNotificationHistory(database); err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to clear notification history", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "notification history cleared"})
	}
}

// GetNotifications returns the signed-in user's unread count plus the most
// recent in-app notifications, for the bell dropdown's initial load (the
// live stream in StreamNotifications handles updates after that).
func GetNotifications(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetInt64("user_id")

		count, lastID, err := db.GetUnreadNotificationCount(database, userID)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to load notifications", err))
			return
		}
		items, err := db.GetInAppNotifications(database, 0, 20)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to load notifications", err))
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
			middleware.HandleError(c, model.NewBadRequest("Invalid request body", err))
			return
		}

		if err := db.MarkNotificationsRead(database, userID, req.LastReadID); err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to mark notifications read", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "marked read"})
	}
}

// StreamNotifications is a Server-Sent Events endpoint: it holds the
// connection open and pushes each new in-app notification as it's
// published, via notifyhub.Hub (which itself fans out over Redis pub/sub
// when running with multiple api replicas).
func StreamNotifications(hub *notifyhub.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			middleware.HandleError(c, model.NewInternal("Streaming unsupported", nil))
			return
		}

		// The server's WriteTimeout (15s, see main.go) exists to bound normal
		// request/response cycles and would otherwise kill this connection
		// out from under us shortly after it opens, well before the client
		// ever sees a second event - nginx logs that as "upstream
		// prematurely closed connection". Disable it for just this
		// connection; every other route keeps the 15s limit.
		_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})

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
