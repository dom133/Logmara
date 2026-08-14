package handler

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"logmara/auth"
	"logmara/rotation"
	"logmara/util"
	"logmara/vaultclient"

	"github.com/gin-gonic/gin"
)

var (
	vaultClientRef       *vaultclient.Client
	authConfigRef        *auth.Config
	vaultEnabled         atomic.Bool
	lastRotationAtRef    atomic.Value
	nextRotationAtRef    atomic.Value
	manualTriggeredRef   atomic.Bool
	secretResults        [4]atomicValue
)

type atomicValue struct {
	v interface{}
}

func (av *atomicValue) Load() interface{} {
	return av.v
}

func (av *atomicValue) Store(v interface{}) {
	av.v = v
}

// SetRotationRefs registers the references needed for the rotation status
// endpoint. Called once at startup.
func SetRotationRefs(vc *vaultclient.Client, authCfg *auth.Config) {
	vaultClientRef = vc
	authConfigRef = authCfg
	if vc != nil {
		vaultEnabled.Store(true)
	}
}

// SetRotationTimestamps updates the global rotation timestamp references.
func SetRotationTimestamps(last, next time.Time) {
	if !last.IsZero() {
		lastRotationAtRef.Store(&last)
	}
	if !next.IsZero() {
		nextRotationAtRef.Store(&next)
	}
}

// SetSecretRotationResult updates the rotation result for a specific secret.
func SetSecretRotationResult(index int, result string, errMsg string) {
	if index >= 0 && index < 4 {
		secretResults[index].Store(rotation.SecretResult{
			Result: result,
			Error:  errMsg,
			Time:   time.Now(),
		})
	}
}

// TriggerManualRotation triggers an immediate rotation of all secrets.
func TriggerManualRotation(ctx context.Context) error {
	if vaultClientRef == nil {
		return nil
	}

	cb := buildRotationCallbacks()
	ch := vaultClientRef.RotateNow(ctx, cb)
	err := <-ch
	if err == nil {
		manualTriggeredRef.Store(true)
	}
	return err
}

func buildRotationCallbacks() vaultclient.RotationCallbacks {
	return vaultclient.RotationCallbacks{
		RotateJWTSecret: func(s string) {
			if authConfigRef != nil {
				authConfigRef.RotateSecret(s)
				SetSecretRotationResult(0, "success", "")
				lastRot := time.Now()
				lastRotationAtRef.Store(&lastRot)
			}
		},
		RotateEncryptionKey: func(s string) {
			util.RotateEncryptionKey(s)
			SetSecretRotationResult(1, "success", "")
			lastRot := time.Now()
			lastRotationAtRef.Store(&lastRot)
		},
		RotatePostgreSQLDSN: func(dsn string) {
			SetSecretRotationResult(2, "pending", "")
			// Actual pool rotation happens in the callback registered in main.go
		},
		RotateRabbitMQURL: func(url string) {
			SetSecretRotationResult(3, "pending", "")
			// Actual queue rotation happens in the callback registered in main.go
		},
	}
}

// GetRotationStatus returns the current rotation status for all secrets.
func GetRotationStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		status := buildRotationStatus()
		c.JSON(http.StatusOK, status)
	}
}

// TriggerRotation triggers an immediate rotation of all secrets.
func TriggerRotation() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()

		err := TriggerManualRotation(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "rotation failed: " + err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "rotation triggered",
			"message": "All secrets rotation has been triggered. Check status endpoint for results.",
		})
	}
}

type RotationStatusResponse struct {
	VaultEnabled     bool                      `json:"vault_enabled"`
	RotationInterval string                    `json:"rotation_interval"`
	LastRotationAt   *string                   `json:"last_rotation_at"`
	NextRotationAt   *string                   `json:"next_rotation_at"`
	ManualTriggered  bool                      `json:"manual_triggered"`
	Secrets          []SecretRotationStatus    `json:"secrets"`
}

type SecretRotationStatus struct {
	Name            string `json:"name"`
	LastRotatedAt   *string `json:"last_rotated_at"`
	LastResult      string  `json:"last_result"`
	LastError       string  `json:"last_error"`
	HasSecondaryKey bool    `json:"has_secondary_key"`
}

func buildRotationStatus() RotationStatusResponse {
	resp := RotationStatusResponse{
		VaultEnabled:  vaultEnabled.Load(),
		RotationInterval: "24h",
		ManualTriggered: manualTriggeredRef.Load(),
		Secrets:       make([]SecretRotationStatus, 4),
	}

	if v := lastRotationAtRef.Load(); v != nil {
		if t, ok := v.(*time.Time); ok {
			s := t.Format(time.RFC3339)
			resp.LastRotationAt = &s
		}
	}

	if v := nextRotationAtRef.Load(); v != nil {
		if t, ok := v.(*time.Time); ok {
			s := t.Format(time.RFC3339)
			resp.NextRotationAt = &s
		}
	}

	// JWT Secret
	resp.Secrets[0] = SecretRotationStatus{
		Name: "JWT Secret",
		LastResult: getSecretResult(0),
		LastError:  getSecretError(0),
	}
	if authConfigRef != nil {
		resp.Secrets[0].HasSecondaryKey = authConfigRef.HasSecondarySecret()
	}

	// Encryption Key
	resp.Secrets[1] = SecretRotationStatus{
		Name: "Encryption Key",
		LastResult: getSecretResult(1),
		LastError:  getSecretError(1),
	}
	resp.Secrets[1].HasSecondaryKey = util.HasSecondaryKey()

	// PostgreSQL Credentials
	resp.Secrets[2] = SecretRotationStatus{
		Name: "PostgreSQL Credentials",
		LastResult: getSecretResult(2),
		LastError:  getSecretError(2),
	}

	// RabbitMQ Credentials
	resp.Secrets[3] = SecretRotationStatus{
		Name: "RabbitMQ Credentials",
		LastResult: getSecretResult(3),
		LastError:  getSecretError(3),
	}

	return resp
}

func getSecretResult(index int) string {
	if v := secretResults[index].Load(); v != nil {
		if r, ok := v.(rotation.SecretResult); ok {
			return r.Result
		}
	}
	return "none"
}

func getSecretError(index int) string {
	if v := secretResults[index].Load(); v != nil {
		if r, ok := v.(rotation.SecretResult); ok {
			return r.Error
		}
	}
	return ""
}
