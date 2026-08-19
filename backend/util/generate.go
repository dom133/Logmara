package util

import (
	"crypto/rand"
	"encoding/hex"
)

func GenerateJWTSecret() string {
	return generateRandomHex(32)
}

func GenerateEncryptionKey() string {
	return generateRandomHex(32)
}

// GenerateTokenHashKey returns a random key suitable for TOKEN_HASH_KEY, the
// HMAC key used to hash refresh tokens and API keys before storage (see
// TokenHash). 32 random bytes -> 64 hex chars, comfortably above the 32-char
// minimum enforced by ResolveTokenHashKey.
func GenerateTokenHashKey() string {
	return generateRandomHex(32)
}

func generateRandomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
