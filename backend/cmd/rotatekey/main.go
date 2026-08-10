package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
	"logmara/util"
)

// Deprecated: This command is no longer needed. The application now handles
// encryption key rotation automatically via dual-key support.
func main() {
	oldKey := os.Getenv("OLD_ENCRYPTION_KEY")
	newKey := os.Getenv("NEW_ENCRYPTION_KEY")
	if oldKey == "" || newKey == "" {
		log.Fatal("OLD_ENCRYPTION_KEY and NEW_ENCRYPTION_KEY must be set")
	}

	util.SetEncryptionKey(oldKey)

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		getEnv("PG_USER", "syslog"),
		os.Getenv("PG_PASSWORD"),
		getEnv("PG_HOST", "haproxy"),
		getEnv("PG_PORT", "5000"),
		getEnv("PG_DB", "syslog_db"),
		getEnv("PG_SSLMODE", "disable"),
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	// Re-encrypt notification_channels.secret
	rows, err := db.Query("SELECT id, secret FROM notification_channels WHERE secret IS NOT NULL AND secret != ''")
	if err != nil {
		log.Fatalf("Failed to query notification_channels: %v", err)
	}
	count := 0
	for rows.Next() {
		var id int
		var secret string
		if err := rows.Scan(&id, &secret); err != nil {
			log.Fatalf("Failed to scan row: %v", err)
		}
		decrypted, err := util.Decrypt(secret)
		if err != nil {
			log.Printf("Warning: failed to decrypt notification channel %d, skipping: %v", id, err)
			continue
		}
		util.SetEncryptionKey(newKey)
		encrypted, err := util.Encrypt(decrypted)
		util.SetEncryptionKey(oldKey)
		if err != nil {
			log.Fatalf("Failed to re-encrypt notification channel %d: %v", id, err)
		}
		if _, err := db.Exec("UPDATE notification_channels SET secret = $1 WHERE id = $2", encrypted, id); err != nil {
			log.Fatalf("Failed to update notification channel %d: %v", id, err)
		}
		count++
	}
	rows.Close()
	log.Printf("Re-encrypted %d notification channels", count)

	// Re-encrypt app_settings for smtp_password and ldap_bind_password
	settings, err := db.Query("SELECT key, value FROM app_settings WHERE key IN ('smtp_password', 'ldap_bind_password') AND value != ''")
	if err != nil {
		log.Fatalf("Failed to query app_settings: %v", err)
	}
	settingsCount := 0
	for settings.Next() {
		var key, value string
		if err := settings.Scan(&key, &value); err != nil {
			log.Fatalf("Failed to scan settings row: %v", err)
		}
		decrypted, err := util.Decrypt(value)
		if err != nil {
			log.Printf("Warning: failed to decrypt setting %s, skipping: %v", key, err)
			continue
		}
		util.SetEncryptionKey(newKey)
		encrypted, err := util.Encrypt(decrypted)
		util.SetEncryptionKey(oldKey)
		if err != nil {
			log.Fatalf("Failed to re-encrypt setting %s: %v", key, err)
		}
		if _, err := db.Exec("UPDATE app_settings SET value = $1 WHERE key = $2", encrypted, key); err != nil {
			log.Fatalf("Failed to update setting %s: %v", key, err)
		}
		settingsCount++
	}
	settings.Close()
	log.Printf("Re-encrypted %d settings", settingsCount)

	log.Println("Encryption key rotation complete")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
