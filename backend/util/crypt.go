package util

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"sync"
)

var ErrInvalidKey = errors.New("invalid encryption key")

var (
	encryptKeyPrimary   string
	encryptKeySecondary string
	encryptMu           sync.RWMutex
)

// SetEncryptionKey sets the primary encryption key. Call once at startup.
func SetEncryptionKey(key string) {
	encryptMu.Lock()
	defer encryptMu.Unlock()
	encryptKeyPrimary = key
}

// RotateEncryptionKey atomically rotates the encryption key. The old primary
// becomes the secondary key (used for decryption during the grace period),
// and the new key becomes the primary key (used for encryption).
func RotateEncryptionKey(newKey string) {
	encryptMu.Lock()
	defer encryptMu.Unlock()
	encryptKeySecondary = encryptKeyPrimary
	encryptKeyPrimary = newKey
	slog.Info("util: encryption key rotated, secondary key set for grace period")
}

// ClearSecondaryEncryptionKey removes the secondary key after the grace
// period has elapsed. Data encrypted with the old key can no longer be
// decrypted.
func ClearSecondaryEncryptionKey() {
	encryptMu.Lock()
	defer encryptMu.Unlock()
	encryptKeySecondary = ""
	slog.Info("util: encryption secondary key cleared")
}

// HasSecondaryKey returns true if a secondary (grace period) key is active.
func HasSecondaryKey() bool {
	encryptMu.RLock()
	defer encryptMu.RUnlock()
	return encryptKeySecondary != ""
}

// Encrypt encrypts plaintext using the primary key.
func Encrypt(plaintext string) (string, error) {
	encryptMu.RLock()
	key := encryptKeyPrimary
	encryptMu.RUnlock()

	derivedKey := deriveKey(key)
	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts ciphertext. It tries the primary key first, then the
// secondary key (grace period), so data encrypted with the old key remains
// readable until it is re-encrypted with the new key.
func Decrypt(ciphertext string) (string, error) {
	encryptMu.RLock()
	primary := encryptKeyPrimary
	secondary := encryptKeySecondary
	encryptMu.RUnlock()

	result, err := decryptWithKey(primary, ciphertext)
	if err == nil {
		return result, nil
	}

	if secondary != "" {
		result2, err2 := decryptWithKey(secondary, ciphertext)
		if err2 == nil {
			return result2, nil
		}
	}

	return "", errors.New("decryption failed with all available keys")
}

func decryptWithKey(key, ciphertext string) (string, error) {
	derivedKey := deriveKey(key)
	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	cipherBytes, err := hex.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(cipherBytes) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce := cipherBytes[:nonceSize]
	ct := cipherBytes[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

func deriveKey(key string) []byte {
	hash := sha256.Sum256([]byte(key))
	return hash[:]
}
