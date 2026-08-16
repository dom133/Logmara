package handler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"logmara/auth"
	"logmara/db"
	"logmara/rotation"
	"logmara/sharedstate"
	"logmara/tailer"
	"logmara/vaultclient"

	"github.com/gin-gonic/gin"
)

var (
	vaultClientRef      *vaultclient.Client
	vaultEnabled        atomic.Bool
	lastRotationAtRef   atomic.Value
	nextRotationAtRef   atomic.Value
	manualTriggeredRef  atomic.Bool
	secretResults       [4]atomic.Value
	secretLastRotatedAt [4]atomic.Value
	secondaryKeyActive  [4]atomic.Bool
	dbRef               *sql.DB
	rotationBroadcaster *sharedstate.Broadcaster
)

const rotationSyncChannel = "rotation:sync"
const rotationTriggerChannel = "rotation:trigger"

var rotationTriggerBroadcaster *sharedstate.Broadcaster

func SetRotationBroadcaster(b *sharedstate.Broadcaster) {
	rotationBroadcaster = b
}

func SetRotationTriggerBroadcaster(b *sharedstate.Broadcaster) {
	rotationTriggerBroadcaster = b
}

// StartRotationSyncSubscriber listens for rotation sync events from other
// replicas and reloads the local rotation state from the database.
func StartRotationSyncSubscriber(ctx context.Context, b *sharedstate.Broadcaster) {
	b.Subscribe(ctx, rotationSyncChannel, func(string) {
		loadRotationStateFromDB()
	})
}

// StartRotationTriggerSubscriber listens for rotation trigger events.
// Only the rotation leader should subscribe to this channel.
func StartRotationTriggerSubscriber(ctx context.Context, b *sharedstate.Broadcaster) {
	b.Subscribe(ctx, rotationTriggerChannel, func(string) {
		slog.Info("rotation: received trigger from other replica")
		TriggerManualRotation()
	})
}

// loadRotationStateFromDB reloads rotation timestamps and results from the
// database so this replica's in-memory state reflects the latest rotation.
func loadRotationStateFromDB() {
	if dbRef == nil {
		return
	}

	secretKeys := []string{
		"secret_rotation_jwt_at",
		"secret_rotation_encryption_at",
		"secret_rotation_pg_at",
		"secret_rotation_rabbitmq_at",
	}
	for i, key := range secretKeys {
		prefix := key[:len(key)-3]
		if ts := db.GetSetting(dbRef, key, ""); ts != "" {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				secretLastRotatedAt[i].Store(&t)
			}
		}
		if result := db.GetSetting(dbRef, prefix+"_result", ""); result != "" {
			secretResults[i].Store(rotation.SecretResult{
				Result: result,
				Time:   time.Time{},
			})
		}
		if sec := db.GetSetting(dbRef, prefix+"_secondary", ""); sec == "true" {
			secondaryKeyActive[i].Store(true)
		}
	}

	if ts := db.GetSetting(dbRef, "secret_rotation_last_at", ""); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			lastRotationAtRef.Store(&t)
		}
	}
	if ts := db.GetSetting(dbRef, "secret_rotation_next_at", ""); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			nextRotationAtRef.Store(&t)
		}
	}
}

// SetRotationRefs registers the references needed for the rotation status
// endpoint. Called once at startup.
func SetRotationRefs(vc *vaultclient.Client, authCfg *auth.Config, database *sql.DB) {
	vaultClientRef = vc
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
func SetSecretRotationResult(index int, result string) {
	if index >= 0 && index < 4 {
		now := time.Now()
		secretResults[index].Store(rotation.SecretResult{
			Result: result,
			Time:   now,
		})
		secretLastRotatedAt[index].Store(&now)
		if dbRef != nil {
			keys := []string{"secret_rotation_jwt", "secret_rotation_encryption", "secret_rotation_pg", "secret_rotation_rabbitmq"}
			prefix := keys[index]
			db.UpdateSetting(dbRef, prefix+"_at", now.Format(time.RFC3339))
			db.UpdateSetting(dbRef, prefix+"_result", result)
		}
		if (index == 0 || index == 1) && result == "success" {
			secondaryKeyActive[index].Store(true)
			if dbRef != nil {
				keys := []string{"secret_rotation_jwt", "secret_rotation_encryption"}
				db.UpdateSetting(dbRef, keys[index]+"_secondary", "true")
			}
		}
		if rotationBroadcaster != nil {
			if err := rotationBroadcaster.Publish(context.Background(), rotationSyncChannel, ""); err != nil {
				slog.Warn("failed to broadcast rotation sync", "error", err)
			}
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

// RestoreSecretResult restores a persisted rotation result into in-memory
// state without writing to the database or broadcasting.
func RestoreSecretResult(index int, result string) {
	if index >= 0 && index < 4 {
		secretResults[index].Store(rotation.SecretResult{
			Result: result,
			Time:   time.Time{},
		})
	}
}

// ClearSecondaryKeyFlag clears the secondary key flag for a secret, persists
// to DB, and broadcasts sync to other replicas.
func ClearSecondaryKeyFlag(index int) {
	if index >= 0 && index < 4 {
		secondaryKeyActive[index].Store(false)
		if dbRef != nil {
			keys := []string{"secret_rotation_jwt", "secret_rotation_encryption"}
			db.UpdateSetting(dbRef, keys[index]+"_secondary", "false")
		}
		if rotationBroadcaster != nil {
			if err := rotationBroadcaster.Publish(context.Background(), rotationSyncChannel, ""); err != nil {
				slog.Warn("failed to broadcast rotation sync", "error", err)
			}
		}
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

// waitRotationComplete blocks until all 4 secrets have a newer last_rotated_at
// timestamp than before the trigger, or until timeout (300s).
func waitRotationComplete(before [4]time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("rotation timed out after 300s")
		case <-ticker.C:
			allDone := true
			for i := 0; i < 4; i++ {
				if v := secretLastRotatedAt[i].Load(); v != nil {
					if t, ok := v.(*time.Time); ok && t.After(before[i]) {
						continue
					}
				}
				allDone = false
				break
			}
			if allDone {
				return nil
			}
		}
	}
}

// GetRotationStatus returns the current rotation status for all secrets.
func GetRotationStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		status := buildRotationStatus()
		c.JSON(http.StatusOK, status)
	}
}

// TriggerRotation triggers rotation via Redis pub/sub (so it reaches the
// rotation leader even if this replica isn't it) and waits for all secrets
// to complete, returning the final rotation status with per-secret results.
//
// Safety net: after publishing to Redis, it also calls TriggerManualRotation()
// directly. If this replica IS the rotation leader, the direct call hits the
// same rotation goroutine (buffered channel, capacity 1, so at most one
// rotation runs). If this replica is NOT the leader, the Redis message reaches
// the leader, and the direct call is a no-op (vaultClientRef is shared, but
// TriggerRotateNow's buffered channel drops duplicate signals). Either way,
// rotation gets triggered.
func TriggerRotation() gin.HandlerFunc {
	return func(c *gin.Context) {
		_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})

		var before [4]time.Time
		for i := 0; i < 4; i++ {
			if v := secretLastRotatedAt[i].Load(); v != nil {
				if t, ok := v.(*time.Time); ok {
					before[i] = *t
				}
			}
		}

		// Publish rotation trigger to reach the leader replica via Redis.
		if rotationTriggerBroadcaster != nil {
			if err := rotationTriggerBroadcaster.Publish(context.Background(), rotationTriggerChannel, ""); err != nil {
				slog.Warn("failed to publish rotation trigger", "error", err)
			}
		}

		// Direct trigger as a safety net — ensures rotation starts even if
		// Redis pub/sub failed or no replica subscribed to the trigger channel.
		TriggerManualRotation()

		if err := waitRotationComplete(before); err != nil {
			slog.Warn("rotation wait timed out", "error", err)
		}

		status := buildRotationStatus()
		c.JSON(http.StatusOK, status)
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
		Name:            "JWT Secret",
		LastResult:      getSecretResult(0),
		HasSecondaryKey: secondaryKeyActive[0].Load(),
	}
	if v := secretLastRotatedAt[0].Load(); v != nil {
		if t, ok := v.(*time.Time); ok {
			s := t.Format(time.RFC3339)
			resp.Secrets[0].LastRotatedAt = &s
		}
	}

	// Encryption Key
	resp.Secrets[1] = SecretRotationStatus{
		Name:            "Encryption Key",
		LastResult:      getSecretResult(1),
		HasSecondaryKey: secondaryKeyActive[1].Load(),
	}
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
	if v := secretLastRotatedAt[index].Load(); v != nil {
		return "success"
	}
	return "none"
}
