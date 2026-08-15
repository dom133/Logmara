package handler

import (
	"net/http"

	"logmara/db"
	"logmara/middleware"
	"logmara/model"

	"github.com/gin-gonic/gin"
)

// GetVAPIDPublicKey returns the server's VAPID public key, generating the
// key pair on first call. The browser needs this to create a push
// subscription via PushManager.subscribe().
func GetVAPIDPublicKey(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		publicKey, _, err := db.GetOrCreateVAPIDKeys(database)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("push.failedToLoadVapidKey", "Failed to load VAPID key", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"public_key": publicKey})
	}
}

// SubscribePush registers (or re-registers) a browser's push subscription
// for the signed-in user.
func SubscribePush(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		var req model.PushSubscribeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
		middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequestBody", "Invalid request body", err))
		return
		}

		userID := c.GetInt64("user_id")
		if err := db.SavePushSubscription(database, userID, req.Endpoint, req.Keys.P256dh, req.Keys.Auth); err != nil {
			middleware.HandleError(c, model.NewInternalKey("push.failedToSaveSubscription", "Failed to save push subscription", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "subscribed"})
	}
}

// UnsubscribePush removes a browser's push subscription, e.g. when the user
// turns push notifications off.
func UnsubscribePush(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		database := pool.Get()
		var req model.PushUnsubscribeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequestBody", "Invalid request body", err))
			return
		}

		if err := db.DeletePushSubscription(database, req.Endpoint); err != nil {
			middleware.HandleError(c, model.NewInternalKey("push.failedToRemoveSubscription", "Failed to remove push subscription", err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "unsubscribed"})
	}
}
