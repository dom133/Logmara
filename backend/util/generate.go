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

func generateRandomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
