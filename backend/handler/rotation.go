package handler

import (
	"database/sql"
	"net/http"
	"sync/atomic"
	"time"

	"logmara/auth"
	"logmara/db"
	"logmara/rotation"
	"logmara/tailer"
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
	secretLastRotatedAt  [4]atomicValue
	dbRef                *sql.DB
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
func SetRotationRefs(vc *vaultclient.Client, authCfg *auth.Config, database *sql.DB) {
	vaultClientRef = vc
	authConfigRef = authCfg
	dbRef = database
	if vc != nil {
		vaultEnabled.Store(true)
	}
}

// SetRotationTimestamps updates the global rotation timestamp references.
func SetRotationTimestamps(last, next time.Time) {
	if !last.IsZero() {
		lastRotationAtRef.Store(&last)
		if dbRef != nil {
			db.UpdateSetting(dbRef, "secret_rotation_last_at", last.Format(time.RFC3339))
		}
	}
	if !next.IsZero() {
		nextRotationAtRef.Store(&next)
		if dbRef != nil {
			db.UpdateSetting(dbRef, "secret_rotation_next_at", next.Format(time.RFC3339))
		}
	}
}

// SetSecretRotationResult updates the rotation result for a specific secret.
func SetSecretRotationResult(index int, result string, errMsg string) {
	if index >= 0 && index < 4 {
		now := time.Now()
		secretResults[index].Store(rotation.SecretResult{
			Result: result,
			Error:  errMsg,
			Time:   now,
		})
		secretLastRotatedAt[index].Store(&now)
		if dbRef != nil && index >= 0 && index < 4 {
			keys := []string{"secret_rotation_jwt_at", "secret_rotation_encryption_at", "secret_rotation_pg_at", "secret_rotation_rabbitmq_at"}
			db.UpdateSetting(dbRef, keys[index], now.Format(time.RFC3339))
		}
	}
}

// SetSecretLastRotatedAt sets the last rotation timestamp for a secret
// (used to restore persisted timestamps on startup).
func SetSecretLastRotatedAt(index int, t time.Time) {
	if index >= 0 && index < 4 {
		secretLastRotatedAt[index].Store(&t)
	}
}

// TriggerManualRotation signals the main rotation goroutine to perform an
// immediate rotation. It does NOT spawn a parallel goroutine with its own
// callbacks — that would rotate the JWT key twice (once from the main
// goroutine, once from here) and invalidate the user's current access token,
// causing a login loop (401 → /login → silent refresh → new token → 401 again).
func TriggerManualRotation() {
	if vaultClientRef == nil {
		return
	}
	vaultClientRef.TriggerRotateNow()
	manualTriggeredRef.Store(true)
}

// GetRotationStatus returns the current rotation status for all secrets.
func GetRotationStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		status := buildRotationStatus()
		c.JSON(http.StatusOK, status)
	}
}

// TriggerRotation signals the main rotation goroutine to rotate all secrets.
func TriggerRotation() gin.HandlerFunc {
	return func(c *gin.Context) {
		TriggerManualRotation()
		c.JSON(http.StatusOK, gin.H{
			"status":  "rotation triggered",
			"message": "Secret rotation has been triggered. Check status endpoint for results.",
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
	Name              string  `json:"name"`
	LastRotatedAt     *string `json:"last_rotated_at"`
	LastResult        string  `json:"last_result"`
	LastError         string  `json:"last_error"`
	HasSecondaryKey   bool    `json:"has_secondary_key"`
	RabbitMQConnected *bool   `json:"rabbitmq_connected,omitempty"`
}

func buildRotationStatus() RotationStatusResponse {
	resp := RotationStatusResponse{
		VaultEnabled:     vaultEnabled.Load(),
		RotationInterval: "24h",
		ManualTriggered:  manualTriggeredRef.Load(),
		Secrets:          make([]SecretRotationStatus, 4),
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

	for i := 0; i < 4; i++ {
		if v := secretLastRotatedAt[i].Load(); v != nil {
			if t, ok := v.(*time.Time); ok {
				s := t.Format(time.RFC3339)
				resp.Secrets[i].LastRotatedAt = &s
			}
		}
	}

	// JWT Secret
	resp.Secrets[0] = SecretRotationStatus{
		Name:       "JWT Secret",
		LastResult: getSecretResult(0),
		LastError:  getSecretError(0),
	}
	if authConfigRef != nil {
		resp.Secrets[0].HasSecondaryKey = authConfigRef.HasSecondarySecret()
	}
	if v := secretLastRotatedAt[0].Load(); v != nil {
		if t, ok := v.(*time.Time); ok {
			s := t.Format(time.RFC3339)
			resp.Secrets[0].LastRotatedAt = &s
		}
	}

	// Encryption Key
	resp.Secrets[1] = SecretRotationStatus{
		Name:       "Encryption Key",
		LastResult: getSecretResult(1),
		LastError:  getSecretError(1),
	}
	resp.Secrets[1].HasSecondaryKey = util.HasSecondaryKey()
	if v := secretLastRotatedAt[1].Load(); v != nil {
		if t, ok := v.(*time.Time); ok {
			s := t.Format(time.RFC3339)
			resp.Secrets[1].LastRotatedAt = &s
		}
	}

	// PostgreSQL Credentials
	resp.Secrets[2] = SecretRotationStatus{
		Name:       "PostgreSQL Credentials",
		LastResult: getSecretResult(2),
		LastError:  getSecretError(2),
	}
	if v := secretLastRotatedAt[2].Load(); v != nil {
		if t, ok := v.(*time.Time); ok {
			s := t.Format(time.RFC3339)
			resp.Secrets[2].LastRotatedAt = &s
		}
	}

	// RabbitMQ Credentials
	resp.Secrets[3] = SecretRotationStatus{
		Name:       "RabbitMQ Credentials",
		LastResult: getSecretResult(3),
		LastError:  getSecretError(3),
	}
	if v := secretLastRotatedAt[3].Load(); v != nil {
		if t, ok := v.(*time.Time); ok {
			s := t.Format(time.RFC3339)
			resp.Secrets[3].LastRotatedAt = &s
		}
	}
	connected := tailer.GetQueueConnected()
	resp.Secrets[3].RabbitMQConnected = &connected

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
