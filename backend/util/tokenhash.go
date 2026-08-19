package util

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
)

// tokenHashKey holds the key used to HMAC secret bearer tokens (refresh
// tokens, API keys) before they're stored in the database. It's set once at
// startup (SetTokenHashKey, after ResolveTokenHashKey validates it) and read
// on every hash computation, so it's an atomic value for safe concurrent
// access from many handler goroutines.
var tokenHashKey atomic.Value // stores a string

// SetTokenHashKey caches the validated TOKEN_HASH_KEY for TokenHash. Called
// once at startup, before any request is served and before the schema
// migration that re-hashes stored values.
func SetTokenHashKey(key string) {
	tokenHashKey.Store(key)
}

func currentTokenHashKey() string {
	if v, ok := tokenHashKey.Load().(string); ok {
		return v
	}
	return ""
}

// ResolveTokenHashKey returns the key used to HMAC refresh tokens and API
// keys before storage, from TOKEN_HASH_KEY / TOKEN_HASH_KEY_FILE (or Vault),
// or an error with remediation. It must be at least 32 characters. Keeping
// this key out of the database means a database dump alone yields neither
// usable session tokens nor usable API keys, only keyed digests.
func ResolveTokenHashKey() (string, error) {
	key := SecretFromEnv("TOKEN_HASH_KEY")
	if key == "" {
		return "", fmt.Errorf("TOKEN_HASH_KEY is not set; generate one (e.g. `openssl rand -hex 32`) and provide it via the TOKEN_HASH_KEY env var or TOKEN_HASH_KEY_FILE - see README")
	}
	if len(key) < 32 {
		return "", fmt.Errorf("TOKEN_HASH_KEY is too short (%d chars); use at least 32 characters", len(key))
	}
	return key, nil
}

// TokenHash returns the digest stored in the database for a secret bearer
// token (a refresh token or an API key). It is hex(HMAC-SHA256(key, token))
// keyed by TOKEN_HASH_KEY. The raw token is never persisted, so a database
// breach does not yield usable tokens. If the key has not been set (which
// must not happen in a running server - it's required at startup), it falls
// back to the legacy unsalted SHA-256 so lookups stay consistent.
func TokenHash(token string) string {
	if key := currentTokenHashKey(); key != "" {
		return TokenHashWithKey(key, token)
	}
	return TokenHashLegacy(token)
}

// TokenHashWithKey returns hex(HMAC-SHA256(key, token)). Exposed so the
// schema migration (which runs in the db package and resolves the key itself)
// hashes stored values with exactly the same scheme the runtime uses.
func TokenHashWithKey(key, token string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

// TokenHashLegacy returns hex(SHA-256(token)) - the pre-HMAC scheme that
// older deployments used for refresh tokens and API keys. Kept for the
// dual-lookup path that still has to match API keys created before the HMAC
// scheme existed (their plaintext can't be recovered to re-hash).
func TokenHashLegacy(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
