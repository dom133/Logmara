package util

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
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

// DecryptWithPrimaryOnly decrypts ciphertext using only the primary key.
// Returns an error if the primary key cannot decrypt the ciphertext.
func DecryptWithPrimaryOnly(ciphertext string) (string, error) {
	encryptMu.RLock()
	primary := encryptKeyPrimary
	encryptMu.RUnlock()

	return decryptWithKey(primary, ciphertext)
}

// ReencryptAllSecrets decrypts all encrypted secrets with the current key
// (primary or secondary) and re-encrypts them with the primary key.
// Returns the number of successfully re-encrypted records, the number of
// failed records, and an error if any critical failure occurred.
func ReencryptAllSecrets(db *sql.DB) (ok, failed int, err error) {
	slog.Info("util: starting re-encryption of all secrets")

	// Re-encrypt notification_channels.secret
	rows, err := db.Query("SELECT id, secret FROM notification_channels WHERE secret IS NOT NULL AND secret != ''")
	if err != nil {
		slog.Error("util: failed to query notification_channels for re-encryption", "error", err)
		return 0, 0, err
	}

	var channelErrs int
	for rows.Next() {
		var id int
		var secret string
		if err := rows.Scan(&id, &secret); err != nil {
			slog.Error("util: failed to scan notification_channel row", "id", id, "error", err)
			channelErrs++
			continue
		}

		decrypted, err := Decrypt(secret)
		if err != nil {
			slog.Error("util: failed to decrypt notification_channel secret", "id", id, "error", err)
			channelErrs++
			continue
		}

		encrypted, err := Encrypt(decrypted)
		if err != nil {
			slog.Error("util: failed to re-encrypt notification_channel secret", "id", id, "error", err)
			channelErrs++
			continue
		}

		if _, err := db.Exec("UPDATE notification_channels SET secret = $1 WHERE id = $2", encrypted, id); err != nil {
			slog.Error("util: failed to update notification_channel secret", "id", id, "error", err)
			channelErrs++
			continue
		}

		// Verify the new value decrypts with primary key only
		if _, err := DecryptWithPrimaryOnly(encrypted); err != nil {
			slog.Error("util: re-encrypted notification_channel secret fails primary-only decrypt", "id", id, "error", err)
			channelErrs++
			continue
		}

		ok++
	}
	rows.Close()
	slog.Info("util: notification_channels re-encryption done", "ok", ok, "failed", channelErrs)

	// Re-encrypt app_settings (smtp_password, ldap_bind_password)
	settings, err := db.Query("SELECT key, value FROM app_settings WHERE key IN ('smtp_password', 'ldap_bind_password') AND value != ''")
	if err != nil {
		slog.Error("util: failed to query app_settings for re-encryption", "error", err)
		return ok, channelErrs, err
	}

	var settingsErrs int
	for settings.Next() {
		var key, value string
		if err := settings.Scan(&key, &value); err != nil {
			slog.Error("util: failed to scan app_settings row", "key", key, "error", err)
			settingsErrs++
			continue
		}

		decrypted, err := Decrypt(value)
		if err != nil {
			slog.Error("util: failed to decrypt app_setting", "key", key, "error", err)
			settingsErrs++
			continue
		}

		encrypted, err := Encrypt(decrypted)
		if err != nil {
			slog.Error("util: failed to re-encrypt app_setting", "key", key, "error", err)
			settingsErrs++
			continue
		}

		if _, err := db.Exec("UPDATE app_settings SET value = $1 WHERE key = $2", encrypted, key); err != nil {
			slog.Error("util: failed to update app_setting", "key", key, "error", err)
			settingsErrs++
			continue
		}

		// Verify the new value decrypts with primary key only
		if _, err := DecryptWithPrimaryOnly(encrypted); err != nil {
			slog.Error("util: re-encrypted app_setting fails primary-only decrypt", "key", key, "error", err)
			settingsErrs++
			continue
		}

		ok++
	}
	settings.Close()

	totalFailed := channelErrs + settingsErrs
	slog.Info("util: app_settings re-encryption done", "ok", ok, "failed", totalFailed)

	if totalFailed > 0 {
		return ok, totalFailed, errors.New("some secrets failed re-encryption")
	}

	slog.Info("util: all secrets re-encrypted successfully", "count", ok)
	return ok, 0, nil
}
