package handler

import (
	"net/http"

	"logmara/cloudbridge"

	"github.com/gin-gonic/gin"
)

// GetCloudBridgeStatus backs the Admin > Cloud Bridge tab - see
// cloudbridge.CurrentStatus for what "enrolled" vs "connected" mean.
func GetCloudBridgeStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cloudbridge.Enabled() {
			c.JSON(http.StatusNotFound, gin.H{"error": "cloud bridge is not enabled"})
			return
		}
		status, err := cloudbridge.CurrentStatus()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load cloud bridge status"})
			return
		}
		c.JSON(http.StatusOK, status)
	}
}

type SubmitCloudBridgeLinkRequest struct {
	Link string `json:"link" binding:"required"`
}

// SubmitCloudBridgeLink is the admin action that actually pairs this
// installation - see cloudbridge.EnrollWithLink for what happens next.
// Only reachable at all while cloud_bridge is enabled and not yet
// enrolled; EnrollWithLink itself refuses a second enrollment. No pool
// param needed - cloudbridge already holds its own reference from Start.
func SubmitCloudBridgeLink() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cloudbridge.Enabled() {
			c.JSON(http.StatusNotFound, gin.H{"error": "cloud bridge is not enabled"})
			return
		}
		var req SubmitCloudBridgeLinkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if err := cloudbridge.EnrollWithLink(req.Link); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}
