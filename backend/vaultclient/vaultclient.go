// Package vaultclient reads secrets directly from HashiCorp Vault's KV v2
// API (secret/data/logmara/<name>), as an alternative to vault-agent
// writing them to a local file. Entirely optional: with VAULT_ADDR unset
// (docker-compose.yml, or any deployment without Vault), Get() returns nil
// and every caller falls back to its own env var / *_FILE source - see
// util.SecretFromEnv.
package vaultclient

import (
	"context"
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

// mountPrefix is the raw KV v2 API path - unlike the `vault kv` CLI, the
// HTTP API always needs the literal "data/" segment (see the path-handling
// notes in scripts/vault-bootstrap.sh and scripts/rotate-secrets.sh).
const mountPrefix = "secret/data/logmara/"

// requestTimeout bounds a single Vault read so a network hiccup can't hang
// whatever request path triggered it.
const requestTimeout = 5 * time.Second

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
