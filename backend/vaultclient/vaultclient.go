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

// requestTimeout bounds a single Vault KV read/write so a network hiccup can't
// hang whatever request path triggered it.
const requestTimeout = 5 * time.Second

// dynamicSecretTimeout bounds a single dynamic-credentials request
// (database, rabbitmq). Generating credentials is slower than a KV read
// because Vault must connect to the backend, execute DDL, and return creds.
const dynamicSecretTimeout = 15 * time.Second

// dynamicSecretMaxRetries is how many times to retry a dynamic-credentials
// request when it fails with context.DeadlineExceeded or Vault 500.
const dynamicSecretMaxRetries = 2

// dynamicFetchMaxRetries is how many times to retry the initial dynamic
// credentials fetch at startup. More retries than rotation since startup
// should tolerate Vault being briefly unavailable.
const dynamicFetchMaxRetries = 5

// dynamicSecretRetryDelay is the wait between retries.
const dynamicSecretRetryDelay = 2 * time.Second

// waitReadyInterval is how long to sleep between seal-status checks.
const waitReadyInterval = 5 * time.Second

// waitReadyTimeout is the maximum time to wait for Vault to become unsealed.
const waitReadyTimeout = 5 * time.Minute

type Client struct {
	api *vaultapi.Client

	mu          sync.Mutex
	cache       map[string]cacheEntry
	rotateNowCh chan struct{}
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

// NewForTest returns a Client built around the given Vault API client,
// bypassing the environment-driven Get() singleton. Intended for tests.
func NewForTest(api *vaultapi.Client) *Client {
	return &Client{api: api, cache: make(map[string]cacheEntry), rotateNowCh: make(chan struct{}, 1)}
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
	return &Client{api: c, cache: make(map[string]cacheEntry), rotateNowCh: make(chan struct{}, 1)}
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

// GetSecretNoCache reads the "value" field of secret/data/logmara/<name>
// (KV v2) directly from Vault, bypassing the cache. Used by the secret sync
// ticker on non-leader replicas to detect when the leader has rotated a
// secret and the local copy needs updating.
func (c *Client) GetSecretNoCache(name string) (value string, ok bool) {
	if c == nil {
		return "", false
	}
	return c.fetch(name)
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
		slog.Info("vault: sealed, waiting", "elapsed", elapsed, "remaining", time.Until(deadline))
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
	OnRotateFailure     func(engine string, errMsg string)
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
		case <-c.rotateNowCh:
			slog.Info("vault: manual rotation triggered")
			c.rotateSecrets(ctx, cb)
		}
	}
}

// RotateNow triggers an immediate rotation of all secrets. Returns a channel
// that receives nil on success or an error if rotation failed. The channel
// is closed after the result is sent.
func (c *Client) RotateNow(ctx context.Context, cb RotationCallbacks) <-chan error {
	if c == nil {
		ch := make(chan error, 1)
		ch <- fmt.Errorf("vault client not configured")
		close(ch)
		return ch
	}

	ch := make(chan error, 1)
	go func() {
		defer close(ch)
		slog.Info("vault: rotating secrets (manual trigger)")
		c.rotateSecrets(ctx, cb)
		ch <- nil
	}()
	return ch
}

// TriggerRotateNow sends a fire-and-forget signal to the rotation goroutine
// to trigger an immediate rotation. No-op if the goroutine isn't running.
func (c *Client) TriggerRotateNow() {
	if c == nil || c.rotateNowCh == nil {
		return
	}
	select {
	case c.rotateNowCh <- struct{}{}:
		slog.Info("vault: manual rotation signal sent to goroutine")
	default:
		slog.Warn("vault: manual rotation channel full, signal dropped")
	}
}

func (c *Client) rotateSecrets(ctx context.Context, cb RotationCallbacks) {
	slog.Info("vault: rotating secrets")

	failures := 0
	wrapFailure := func(engine string, errMsg string) {
		failures++
		if cb.OnRotateFailure != nil {
			cb.OnRotateFailure(engine, errMsg)
		}
	}

	// Rotate JWT secret
	newJWTSecret, err := generateRandomKey(32)
	if err != nil {
		slog.Error("vault: failed to generate JWT secret", "error", err)
		wrapFailure("jwt_secret", err.Error())
	} else if err := c.WriteSecret("jwt_secret", newJWTSecret); err != nil {
		slog.Error("vault: failed to write JWT secret", "error", err)
		wrapFailure("jwt_secret", err.Error())
	} else {
		cb.RotateJWTSecret(newJWTSecret)
	}

	// Rotate encryption key
	newEncKey, err := generateRandomKey(32)
	if err != nil {
		slog.Error("vault: failed to generate encryption key", "error", err)
		wrapFailure("encryption_key", err.Error())
	} else if err := c.WriteSecret("encryption_key", newEncKey); err != nil {
		slog.Error("vault: failed to write encryption key", "error", err)
		wrapFailure("encryption_key", err.Error())
	} else {
		cb.RotateEncryptionKey(newEncKey)
	}

	// Rotate dynamic PostgreSQL credentials
	c.rotateDynamicSecret(ctx, "database", cb.RotatePostgreSQLDSN, func(engine string, errMsg string) {
		failures++
		if cb.OnRotateFailure != nil {
			cb.OnRotateFailure(engine, errMsg)
		}
	})

	// Redis has no dynamic-secret rotation: neither Vault Redis plugin
	// discovers the current master through Sentinel, and unilaterally
	// minting a new password wouldn't work anyway since redis1/2/3
	// themselves enforce it, not api - see the comment in
	// scripts/vault-bootstrap.sh's setup-dynamic-secrets. Redis password
	// rotation stays a manual scripts/rotate-secrets.sh operation.

	// Rotate dynamic RabbitMQ credentials
	c.rotateDynamicSecret(ctx, "rabbitmq", cb.RotateRabbitMQURL, func(engine string, errMsg string) {
		failures++
		if cb.OnRotateFailure != nil {
			cb.OnRotateFailure(engine, errMsg)
		}
	})

	if failures == 4 {
		slog.Error("vault: all secret rotations failed", "failures", failures)
	} else if failures > 0 {
		slog.Warn("vault: secrets rotation complete with failures", "failures", failures, "total", 4)
	} else {
		slog.Info("vault: secrets rotation complete")
	}
}

// dynamicRoleName is the Vault role name provisioned by
// scripts/vault-bootstrap.sh setup-dynamic-secrets for all three dynamic
// secrets engines (secret-dynamic/{database,redis,rabbitmq}/roles/logmara-app).
const dynamicRoleName = "logmara-app"

func (c *Client) rotateDynamicSecret(ctx context.Context, engine string, callback func(string), onFailure func(string, string)) {
	if c == nil || callback == nil {
		return
	}

	slog.Info("vault: rotating dynamic secret", "engine", engine)

	// Dynamic secrets engines issue credentials via <mount>/creds/<role>,
	// not the mount root - reading "secret-dynamic/<engine>" directly
	// always 404s.
	path := "secret-dynamic/" + engine + "/creds/" + dynamicRoleName

	var secret *vaultapi.Secret
	var err error

	maxAttempts := 1 + dynamicSecretMaxRetries
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			slog.Info("vault: retrying dynamic secret rotation", "engine", engine, "attempt", attempt+1, "max", maxAttempts)
			time.Sleep(dynamicSecretRetryDelay)
		}

		ctx2, cancel := context.WithTimeout(ctx, dynamicSecretTimeout)
		secret, err = c.api.Logical().ReadWithContext(ctx2, path)
		cancel()

		if err == nil {
			break
		}

		// Retry on deadline exceeded or Vault 500 (transient backend errors
		// like PostgreSQL "tuple concurrently updated"). Other errors are
		// likely misconfiguration and won't be fixed by retrying.
		isRetryable := (err == context.DeadlineExceeded) || strings.Contains(err.Error(), "Code: 500")
		if isRetryable && attempt < dynamicSecretMaxRetries {
			slog.Warn("vault: dynamic secret rotation failed, will retry", "engine", engine, "attempt", attempt+1, "error", err)
			continue
		}

		slog.Warn("vault: failed to rotate dynamic secret", "engine", engine, "error", err)
		if onFailure != nil {
			onFailure(engine, err.Error())
		}
		return
	}

	if secret == nil || secret.Data == nil {
		slog.Warn("vault: no dynamic secret returned", "engine", engine, "path", path)
		if onFailure != nil {
			onFailure(engine, "no secret data returned from Vault")
		}
		return
	}

	// Log available data keys for debugging (without values)
	keys := make([]string, 0, len(secret.Data))
	for k := range secret.Data {
		keys = append(keys, k)
	}
	slog.Debug("vault: dynamic secret data keys", "engine", engine, "keys", keys)

	var newValue string
	switch engine {
	case "database":
		username, _ := secret.Data["username"].(string)
		password, _ := secret.Data["password"].(string)
		if username != "" && password != "" {
			host := os.Getenv("POSTGRES_HOST")
			port := os.Getenv("POSTGRES_PORT")
			dbname := os.Getenv("POSTGRES_DB")
			sslmode := os.Getenv("POSTGRES_SSLMODE")
			if sslmode == "" {
				sslmode = "disable"
			}
			newValue = "postgres://" + username + ":" + password + "@" + host + ":" + port + "/" + dbname + "?sslmode=" + sslmode

				// Persist credentials to Vault KV so non-leader replicas can
			// sync them via the secret sync ticker.
			c.WriteSecret("pg_dynamic_username", username)
			c.WriteSecret("pg_dynamic_password", password)
			c.WriteSecret("pg_dynamic_version", fmt.Sprintf("%d", time.Now().Unix()))
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
		if newValue != "" {
			c.WriteSecret("rabbitmq_dynamic_url", newValue)
			c.WriteSecret("rabbitmq_dynamic_version", fmt.Sprintf("%d", time.Now().Unix()))
		}
	}

	if newValue != "" {
		callback(newValue)
		slog.Info("vault: dynamic secret rotated", "engine", engine)
	} else {
		slog.Warn("vault: dynamic secret rotation produced no value", "engine", engine, "data_keys", keys)
		if onFailure != nil {
			onFailure(engine, "rotation produced no value")
		}
	}
}

// DynamicCredentials holds the DSN and RabbitMQ URL fetched from Vault's
// dynamic secrets engines.
type DynamicCredentials struct {
	PGDSN     string
	RabbitMQ  string
}

// FetchDynamicCredentials fetches initial dynamic credentials for PostgreSQL
// and RabbitMQ from Vault. It retries on transient errors (deadline exceeded,
// Vault 500) up to dynamicFetchMaxRetries times. Returns an error if Vault
// is unavailable after all retries.
//
// After fetching, it writes the credentials and a version marker to Vault KV
// so non-leader replicas can sync them via the secret sync ticker.
//
// Returns nil (not an error) if Vault is not configured (c is nil), so
// callers can fall back to static credentials or the setup wizard.
func (c *Client) FetchDynamicCredentials(ctx context.Context) (*DynamicCredentials, error) {
	if c == nil {
		return nil, nil
	}

	pgDSN, pgUser, pgPass, err := c.fetchDynamicCredentialWithParts(ctx, "database")
	if err != nil {
		return nil, fmt.Errorf("fetch PG dynamic credentials: %w", err)
	}
	c.WriteSecret("pg_dynamic_username", pgUser)
	c.WriteSecret("pg_dynamic_password", pgPass)
	c.WriteSecret("pg_dynamic_version", fmt.Sprintf("%d", time.Now().Unix()))

	rmqURL, err := c.fetchDynamicCredential(ctx, "rabbitmq")
	if err != nil {
		return nil, fmt.Errorf("fetch RabbitMQ dynamic credentials: %w", err)
	}
	c.WriteSecret("rabbitmq_dynamic_url", rmqURL)
	c.WriteSecret("rabbitmq_dynamic_version", fmt.Sprintf("%d", time.Now().Unix()))

	slog.Info("vault: dynamic credentials fetched at startup", "engines", "database,rabbitmq")
	return &DynamicCredentials{PGDSN: pgDSN, RabbitMQ: rmqURL}, nil
}

func (c *Client) fetchDynamicCredentialWithParts(ctx context.Context, engine string) (dsn, username, password string, err error) {
	path := "secret-dynamic/" + engine + "/creds/" + dynamicRoleName

	maxAttempts := 1 + dynamicFetchMaxRetries
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			slog.Info("vault: retrying dynamic credential fetch", "engine", engine, "attempt", attempt+1, "max", maxAttempts)
			time.Sleep(dynamicSecretRetryDelay)
		}

		ctx2, cancel := context.WithTimeout(ctx, dynamicSecretTimeout)
		secret, fetchErr := c.api.Logical().ReadWithContext(ctx2, path)
		cancel()

		if fetchErr == nil {
			dsn, username, password, err = buildDynamicValueWithParts(engine, secret)
			return
		}

		isRetryable := (fetchErr == context.DeadlineExceeded) || strings.Contains(fetchErr.Error(), "Code: 500")
		if isRetryable && attempt < dynamicFetchMaxRetries {
			slog.Warn("vault: dynamic credential fetch failed, will retry", "engine", engine, "attempt", attempt+1, "error", fetchErr)
			continue
		}

		slog.Error("vault: failed to fetch dynamic credential", "engine", engine, "error", fetchErr)
		return "", "", "", fetchErr
	}

	return "", "", "", fmt.Errorf("exhausted retries fetching %s credentials", engine)
}

func (c *Client) fetchDynamicCredential(ctx context.Context, engine string) (string, error) {
	path := "secret-dynamic/" + engine + "/creds/" + dynamicRoleName

	maxAttempts := 1 + dynamicFetchMaxRetries
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			slog.Info("vault: retrying dynamic credential fetch", "engine", engine, "attempt", attempt+1, "max", maxAttempts)
			time.Sleep(dynamicSecretRetryDelay)
		}

		ctx2, cancel := context.WithTimeout(ctx, dynamicSecretTimeout)
		secret, err := c.api.Logical().ReadWithContext(ctx2, path)
		cancel()

		if err == nil {
			return buildDynamicValue(engine, secret)
		}

		isRetryable := (err == context.DeadlineExceeded) || strings.Contains(err.Error(), "Code: 500")
		if isRetryable && attempt < dynamicFetchMaxRetries {
			slog.Warn("vault: dynamic credential fetch failed, will retry", "engine", engine, "attempt", attempt+1, "error", err)
			continue
		}

		slog.Error("vault: failed to fetch dynamic credential", "engine", engine, "error", err)
		return "", err
	}

	return "", fmt.Errorf("exhausted retries fetching %s credentials", engine)
}

func buildDynamicValue(engine string, secret *vaultapi.Secret) (string, error) {
	if secret == nil || secret.Data == nil {
		return "", fmt.Errorf("no secret data returned from Vault")
	}

	switch engine {
	case "database":
		username, _ := secret.Data["username"].(string)
		password, _ := secret.Data["password"].(string)
		if username == "" || password == "" {
			return "", fmt.Errorf("missing username or password from Vault")
		}
		host := os.Getenv("POSTGRES_HOST")
		port := os.Getenv("POSTGRES_PORT")
		dbname := os.Getenv("POSTGRES_DB")
		sslmode := os.Getenv("POSTGRES_SSLMODE")
		if host == "" {
			return "", fmt.Errorf("POSTGRES_HOST not set")
		}
		if port == "" {
			port = "5432"
		}
		if dbname == "" {
			dbname = "syslog_db"
		}
		if sslmode == "" {
			sslmode = "disable"
		}
		return "postgres://" + username + ":" + password + "@" + host + ":" + port + "/" + dbname + "?sslmode=" + sslmode, nil

	case "rabbitmq":
		connectionURL, _ := secret.Data["connection_url"].(string)
		if connectionURL != "" {
			return connectionURL, nil
		}
		username, _ := secret.Data["username"].(string)
		password, _ := secret.Data["password"].(string)
		host := os.Getenv("RABBITMQ_HOST")
		port := os.Getenv("RABBITMQ_PORT")
		if username == "" || password == "" || host == "" {
			return "", fmt.Errorf("missing RabbitMQ credentials or host")
		}
		if port == "" {
			port = "5672"
		}
		return "amqp://" + username + ":" + password + "@" + host + ":" + port, nil

	default:
		return "", fmt.Errorf("unknown engine: %s", engine)
	}
}

func buildDynamicValueWithParts(engine string, secret *vaultapi.Secret) (dsn, username, password string, err error) {
	if secret == nil || secret.Data == nil {
		return "", "", "", fmt.Errorf("no secret data returned from Vault")
	}

	switch engine {
	case "database":
		username, _ = secret.Data["username"].(string)
		password, _ = secret.Data["password"].(string)
		if username == "" || password == "" {
			return "", "", "", fmt.Errorf("missing username or password from Vault")
		}
		host := os.Getenv("POSTGRES_HOST")
		port := os.Getenv("POSTGRES_PORT")
		dbname := os.Getenv("POSTGRES_DB")
		sslmode := os.Getenv("POSTGRES_SSLMODE")
		if host == "" {
			return "", "", "", fmt.Errorf("POSTGRES_HOST not set")
		}
		if port == "" {
			port = "5432"
		}
		if dbname == "" {
			dbname = "syslog_db"
		}
		if sslmode == "" {
			sslmode = "disable"
		}
		return "postgres://" + username + ":" + password + "@" + host + ":" + port + "/" + dbname + "?sslmode=" + sslmode, username, password, nil

	default:
		return "", "", "", fmt.Errorf("buildDynamicValueWithParts only supports database engine")
	}
}
