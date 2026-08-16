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

// SubmitCloudBridgeLink is the admin action that pairs this installation -
// see cloudbridge.EnrollWithLink for what happens next. Only reachable at
// all while cloud_bridge is enabled and not yet enrolled; EnrollWithLink
// itself refuses a second enrollment. No pool param needed - cloudbridge
// already holds its own reference from Start.
//
// Pairing alone doesn't connect the tunnel - the response includes the
// certs Logmara Cloud handed back so the frontend can pre-fill the
// certificate panel it shows next; SaveCloudBridgeCertificates is what
// actually persists them and starts the tunnel, once the admin submits it.
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
		state, err := cloudbridge.EnrollWithLink(req.Link)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":          true,
			"ca_cert":     state.CACert,
			"client_cert": state.ClientCert,
			"client_key":  state.ClientKey,
		})
	}
}

type SaveCloudBridgeCertificatesRequest struct {
	CACert     string `json:"ca_cert" binding:"required"`
	ClientCert string `json:"client_cert" binding:"required"`
	ClientKey  string `json:"client_key" binding:"required"`
}

// SaveCloudBridgeCertificates persists this installation's mTLS
// certificate material and (re)connects the tunnel - see
// cloudbridge.SaveCertificates. Used both for the initial save right after
// pairing and later as a repair path if a bad cert needs replacing (unlike
// pairing, this can be called more than once).
func SaveCloudBridgeCertificates() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cloudbridge.Enabled() {
			c.JSON(http.StatusNotFound, gin.H{"error": "cloud bridge is not enabled"})
			return
		}
		var req SaveCloudBridgeCertificatesRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if err := cloudbridge.SaveCertificates(req.CACert, req.ClientCert, req.ClientKey); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// DisconnectCloudBridge is the admin action that leaves Cloud Bridge
// entirely - see cloudbridge.Disconnect. Afterward this installation shows
// as not enrolled again and a new pairing link can be submitted.
func DisconnectCloudBridge() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cloudbridge.Enabled() {
			c.JSON(http.StatusNotFound, gin.H{"error": "cloud bridge is not enabled"})
			return
		}
		if err := cloudbridge.Disconnect(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}
