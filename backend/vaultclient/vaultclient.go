// Package vaultclient reads secrets directly from HashiCorp Vault's KV v2
// API (secret/data/logmara/<name>), as an alternative to vault-agent
// writing them to a local file. Entirely optional: with VAULT_ADDR unset
// (docker-compose.yml, or any deployment without Vault), Get() returns nil
// and every caller falls back to its own env var / *_FILE source - see
// util.SecretFromEnv.
package vaultclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	vaultapi "github.com/hashicorp/vault/api"
)

// cacheTTL bounds how stale a cached secret can be. Long enough that
// callers hitting this on every HTTP request (e.g. ENCRYPTION_KEY,
// RabbitMQ URL - see util.SecretFromEnv's call sites) don't hammer Vault;
// short enough that scripts/rotate-secrets.sh takes effect without an api
// restart.
const cacheTTL = 30 * time.Second

// rotationInterval is how often the application rotates its own secrets
// (JWT signing key, encryption key). Dynamic secrets (PG, Redis, RabbitMQ)
// are rotated on the same schedule.
const rotationInterval = 24 * time.Hour

// mountPrefix is the raw KV v2 API path - unlike the `vault kv` CLI, the
// HTTP API always needs the literal "data/" segment (see the path-handling
// notes in scripts/vault-bootstrap.sh and scripts/rotate-secrets.sh).
const mountPrefix = "secret/data/logmara/"

// requestTimeout bounds a single Vault read so a network hiccup can't hang
// whatever request path triggered it.
const requestTimeout = 5 * time.Second

// waitReadyInterval is how long to sleep between seal-status checks.
const waitReadyInterval = 5 * time.Second

// waitReadyTimeout is the maximum time to wait for Vault to become unsealed.
const waitReadyTimeout = 5 * time.Minute

type Client struct {
	api *vaultapi.Client

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	value   string
	ok      bool
	fetched time.Time
}

var (
	instance *Client
	initOnce sync.Once
)

// Get returns the process-wide Vault client, initialized from the
// environment on first call. Safe to call on a nil result - GetSecret on a
// nil *Client always returns ok=false.
func Get() *Client {
	initOnce.Do(func() {
		instance = newClient()
	})
	return instance
}

func newClient() *Client {
	addr := strings.TrimSpace(os.Getenv("VAULT_ADDR"))
	if addr == "" {
		return nil
	}

	token := strings.TrimSpace(os.Getenv("VAULT_TOKEN"))
	if token == "" {
		if p := strings.TrimSpace(os.Getenv("VAULT_TOKEN_FILE")); p != "" {
			if b, err := os.ReadFile(p); err == nil {
				token = strings.TrimSpace(string(b))
			} else {
				slog.Warn("vault: could not read VAULT_TOKEN_FILE", "path", p, "error", err)
			}
		}
	}
	if token == "" {
		slog.Warn("vault: VAULT_ADDR set but no VAULT_TOKEN/VAULT_TOKEN_FILE - Vault-backed secrets disabled, falling back to env/file")
		return nil
	}

	cfg := vaultapi.DefaultConfig()
	cfg.Address = addr
	c, err := vaultapi.NewClient(cfg)
	if err != nil {
		slog.Error("vault: failed to create client", "error", err)
		return nil
	}
	c.SetToken(token)

	slog.Info("vault: enabled", "addr", addr)
	return &Client{api: c, cache: make(map[string]cacheEntry)}
}

// GetSecret reads the "value" field of secret/data/logmara/<name> (KV v2),
// serving a cached copy for up to cacheTTL. ok is false if Vault isn't
// configured, the secret doesn't exist, or the read failed - callers
// should fall back to their own source in that case.
func (c *Client) GetSecret(name string) (value string, ok bool) {
	if c == nil {
		return "", false
	}

	c.mu.Lock()
	if entry, exists := c.cache[name]; exists && time.Since(entry.fetched) < cacheTTL {
		c.mu.Unlock()
		return entry.value, entry.ok
	}
	c.mu.Unlock()

	value, ok = c.fetch(name)

	c.mu.Lock()
	c.cache[name] = cacheEntry{value: value, ok: ok, fetched: time.Now()}
	c.mu.Unlock()

	return value, ok
}

func (c *Client) fetch(name string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	secret, err := c.api.Logical().ReadWithContext(ctx, mountPrefix+name)
	if err != nil {
		slog.Warn("vault: read failed", "name", name, "error", err)
		return "", false
	}
	if secret == nil || secret.Data == nil {
		return "", false
	}
	data, ok := secret.Data["data"].(map[string]interface{})
	if !ok {
		return "", false
	}
	value, ok := data["value"].(string)
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

// WaitUntilReady polls Vault's seal-status endpoint until Vault reports
// sealed=false or the timeout is reached. It is a no-op if Vault isn't
// configured (c is nil). Returns an error only on timeout so callers can
// fail-fast instead of proceeding with empty secrets.
func (c *Client) WaitUntilReady() error {
	if c == nil {
		return nil
	}

	deadline := time.Now().Add(waitReadyTimeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		secret, err := c.api.Logical().ReadWithContext(ctx, "sys/seal-status")
		cancel()

		if err == nil && secret != nil {
			sealed, _ := secret.Data["sealed"].(bool)
			if !sealed {
				slog.Info("vault: ready (unsealed)")
				return nil
			}
		}

		elapsed := time.Since(deadline.Add(-waitReadyTimeout))
		slog.Info("vault: sealed, waiting", "elapsed", elapsed, "remaining", deadline.Sub(time.Now()))
		time.Sleep(waitReadyInterval)
	}

	return fmt.Errorf("vault did not become unsealed within %v", waitReadyTimeout)
}

// WriteSecret writes a new value to secret/data/logmara/<name> (KV v2).
// Returns an error if Vault is not configured or the write fails.
func (c *Client) WriteSecret(name, value string) error {
	if c == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	_, err := c.api.Logical().WriteWithContext(ctx, mountPrefix+name, map[string]interface{}{
		"data": map[string]interface{}{
			"value": value,
		},
	})
	if err != nil {
		slog.Error("vault: write failed", "name", name, "error", err)
		return err
	}

	c.mu.Lock()
	if entry, exists := c.cache[name]; exists {
		entry.value = value
		entry.ok = true
		entry.fetched = time.Now()
		c.cache[name] = entry
	}
	c.mu.Unlock()

	slog.Info("vault: secret written", "name", name)
	return nil
}

// generateRandomKey generates a cryptographically secure random hex key.
func generateRandomKey(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// RotationCallbacks holds the functions to call when secrets are rotated.
// There is no RotateRedisPassword: Redis password rotation can't go through
// this automatic path - see rotateSecrets.
type RotationCallbacks struct {
	RotateJWTSecret     func(newSecret string)
	RotateEncryptionKey func(newKey string)
	RotateRabbitMQURL   func(newURL string)
	RotatePostgreSQLDSN func(newDSN string)
}

// StartRotation starts a background goroutine that rotates application
// secrets (JWT, encryption key) every rotationInterval. It also requests
// new dynamic credentials for PostgreSQL and RabbitMQ from Vault.
func (c *Client) StartRotation(ctx context.Context, cb RotationCallbacks) {
	if c == nil || cb.RotateJWTSecret == nil {
		return
	}

	ticker := time.NewTicker(rotationInterval)
	defer ticker.Stop()

	slog.Info("vault: rotation goroutine started", "interval", rotationInterval)

	for {
		select {
		case <-ctx.Done():
			slog.Info("vault: rotation goroutine stopped")
			return
		case <-ticker.C:
			c.rotateSecrets(ctx, cb)
		}
	}
}

func (c *Client) rotateSecrets(ctx context.Context, cb RotationCallbacks) {
	slog.Info("vault: rotating secrets")

	// Rotate JWT secret
	newJWTSecret, err := generateRandomKey(32)
	if err == nil {
		if err := c.WriteSecret("jwt_secret", newJWTSecret); err == nil {
			cb.RotateJWTSecret(newJWTSecret)
		}
	}

	// Rotate encryption key
	newEncKey, err := generateRandomKey(32)
	if err == nil {
		if err := c.WriteSecret("encryption_key", newEncKey); err == nil {
			cb.RotateEncryptionKey(newEncKey)
		}
	}

	// Rotate dynamic PostgreSQL credentials
	c.rotateDynamicSecret(ctx, "database", cb.RotatePostgreSQLDSN)

	// Redis has no dynamic-secret rotation: neither Vault Redis plugin
	// discovers the current master through Sentinel, and unilaterally
	// minting a new password wouldn't work anyway since redis1/2/3
	// themselves enforce it, not api - see the comment in
	// scripts/vault-bootstrap.sh's setup-dynamic-secrets. Redis password
	// rotation stays a manual scripts/rotate-secrets.sh operation.

	// Rotate dynamic RabbitMQ credentials
	c.rotateDynamicSecret(ctx, "rabbitmq", cb.RotateRabbitMQURL)

	slog.Info("vault: secrets rotation complete")
}

// dynamicRoleName is the Vault role name provisioned by
// scripts/vault-bootstrap.sh setup-dynamic-secrets for all three dynamic
// secrets engines (secret-dynamic/{database,redis,rabbitmq}/roles/logmara-app).
const dynamicRoleName = "logmara-app"

func (c *Client) rotateDynamicSecret(ctx context.Context, engine string, callback func(string)) {
	if c == nil || callback == nil {
		return
	}

	ctx2, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	// Dynamic secrets engines issue credentials via <mount>/creds/<role>,
	// not the mount root - reading "secret-dynamic/<engine>" directly
	// always 404s.
	path := "secret-dynamic/" + engine + "/creds/" + dynamicRoleName
	secret, err := c.api.Logical().ReadWithContext(ctx2, path)
	if err != nil {
		slog.Warn("vault: failed to rotate dynamic secret", "engine", engine, "error", err)
		return
	}
	if secret == nil || secret.Data == nil {
		slog.Warn("vault: no dynamic secret returned", "engine", engine)
		return
	}

	var newValue string
	switch engine {
	case "database":
		username, _ := secret.Data["username"].(string)
		password, _ := secret.Data["password"].(string)
		if username != "" && password != "" {
			host := os.Getenv("PG_HOST")
			port := os.Getenv("PG_PORT")
			dbname := os.Getenv("PG_DB")
			sslmode := os.Getenv("PG_SSLMODE")
			newValue = "postgres://" + username + ":" + password + "@" + host + ":" + port + "/" + dbname + "?sslmode=" + sslmode
		}
	case "redis":
		newValue, _ = secret.Data["password"].(string)
	case "rabbitmq":
		newValue, _ = secret.Data["connection_url"].(string)
		if newValue == "" {
			username, _ := secret.Data["username"].(string)
			password, _ := secret.Data["password"].(string)
			host := os.Getenv("RABBITMQ_HOST")
			port := os.Getenv("RABBITMQ_PORT")
			if username != "" && password != "" {
				newValue = "amqp://" + username + ":" + password + "@" + host + ":" + port
			}
		}
	}

	if newValue != "" {
		callback(newValue)
		slog.Info("vault: dynamic secret rotated", "engine", engine)
	}
}
