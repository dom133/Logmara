package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"

	"syslog-gui/db/parsers"
)

func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open db: %w", err)
	}

	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	for i := 0; i < 5; i++ {
		if err := db.Ping(); err == nil {
			return db, nil
		}
		log.Printf("Waiting for database... attempt %d", i+1)
		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("could not connect to database after 5 attempts")
}

func Migrate(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS syslog_logs (
			id BIGSERIAL PRIMARY KEY,
			timestamp TIMESTAMPTZ NOT NULL,
			hostname VARCHAR(255) NOT NULL,
			app_name VARCHAR(255),
			process_id VARCHAR(50),
			msg_id VARCHAR(50),
			severity VARCHAR(20) NOT NULL,
			facility VARCHAR(50),
			message TEXT NOT NULL,
			raw_message TEXT,
			parsed_fields JSONB DEFAULT '{}',
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_timestamp ON syslog_logs (timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_hostname ON syslog_logs (hostname)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_severity ON syslog_logs (severity)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_app_name ON syslog_logs (app_name)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_composite ON syslog_logs (timestamp DESC, severity, hostname)`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='syslog_logs' AND column_name='parsed_fields') THEN ALTER TABLE syslog_logs ADD COLUMN parsed_fields JSONB DEFAULT '{}'; END IF; END $$`,
		`DROP INDEX IF EXISTS idx_syslog_parsed_fields`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_parsed_fields ON syslog_logs USING GIN (parsed_fields)`,
		`CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			username VARCHAR(100) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			is_admin BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS alerts (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			description TEXT,
			condition TEXT NOT NULL,
			threshold INTEGER NOT NULL,
			window_minutes INTEGER DEFAULT 5,
			is_active BOOLEAN DEFAULT TRUE,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS parsers (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			description TEXT,
			device_type VARCHAR(100) NOT NULL,
			match_type VARCHAR(50) NOT NULL,
			match_value VARCHAR(500),
			regex TEXT NOT NULL,
			enabled BOOLEAN DEFAULT TRUE,
			is_builtin BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='parsers' AND column_name='updated_at') THEN ALTER TABLE parsers ADD COLUMN updated_at TIMESTAMPTZ DEFAULT NOW(); END IF; END $$`,
		`CREATE TABLE IF NOT EXISTS parsed_fields_registry (
			id SERIAL PRIMARY KEY,
			parser_id INTEGER REFERENCES parsers(id) ON DELETE CASCADE,
			field_name VARCHAR(100) NOT NULL,
			field_label VARCHAR(255) NOT NULL,
			field_type VARCHAR(50) DEFAULT 'string',
			UNIQUE(parser_id, field_name)
		)`,
		`CREATE TABLE IF NOT EXISTS dashboards (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			description TEXT,
			owner_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
			pinned BOOLEAN DEFAULT FALSE,
			config JSONB NOT NULL DEFAULT '{"devices":[],"fields":[],"filters":{}}',
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='dashboards' AND column_name='pinned') THEN ALTER TABLE dashboards ADD COLUMN pinned BOOLEAN DEFAULT FALSE; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name='role') THEN ALTER TABLE users ADD COLUMN role VARCHAR(50) DEFAULT 'viewer'; END IF; END $$`,
		`UPDATE users SET role = 'admin' WHERE is_admin = TRUE AND role = 'viewer'`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name='is_active') THEN ALTER TABLE users ADD COLUMN is_active BOOLEAN DEFAULT TRUE; END IF; END $$`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key VARCHAR(100) PRIMARY KEY,
			value TEXT NOT NULL,
			description TEXT
		)`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migration failed (%s): %w", stmt[:50], err)
		}
	}

	if err := seedParsers(db); err != nil {
		log.Printf("Warning: seeding parsers failed: %v", err)
	}

	if err := seedSettings(db); err != nil {
		log.Printf("Warning: seeding settings failed: %v", err)
	}

	log.Println("Database migration completed")
	return nil
}

func seedParsers(db *sql.DB) error {
	count := 0
	db.QueryRow("SELECT count(*) FROM parsers WHERE is_builtin").Scan(&count)
	if count > 0 {
		return nil
	}

	allParsers := append([]parsers.ParserSeed{}, parsers.MikroTikParsers...)
	allParsers = append(allParsers, parsers.UbiquitiParsers...)
	allParsers = append(allParsers, parsers.CiscoParsers...)
	allParsers = append(allParsers, parsers.PaloAltoParsers...)
	allParsers = append(allParsers, parsers.PfSenseParsers...)
	allParsers = append(allParsers, parsers.LinuxParsers...)
	allParsers = append(allParsers, parsers.GenericParsers...)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	insertParser := `INSERT INTO parsers (name, description, device_type, match_type, match_value, regex, enabled, is_builtin)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`
	insertField := `INSERT INTO parsed_fields_registry (parser_id, field_name, field_label, field_type)
		VALUES ($1, $2, $3, $4)`

	for _, p := range allParsers {
		var parserID int64
		desc := nullStrPtr(p.Description)
		matchVal := nullStrPtr(p.MatchValue)

		err := tx.QueryRow(insertParser, p.Name, desc, p.DeviceType, p.MatchType, matchVal, p.Regex, true, true).
			Scan(&parserID)
		if err != nil {
			return fmt.Errorf("seed parser %s: %w", p.Name, err)
		}

		for _, f := range p.Fields {
			if _, err := tx.Exec(insertField, parserID, f.Name, f.Label, f.Type); err != nil {
				return fmt.Errorf("seed field %s for parser %s: %w", f.Name, p.Name, err)
			}
		}
	}

	return tx.Commit()
}

func nullStrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func seedSettings(db *sql.DB) error {
	settings := map[string]string{
		"retention_days":   "30",
		"default_role":     "viewer",
		"jwt_expiry":       "24",
		"is_initialized":   "false",
		"ldap_enabled":     "false",
		"ldap_server":      "",
		"ldap_port":        "389",
		"ldap_use_tls":     "false",
		"ldap_base_dn":     "",
		"ldap_bind_dn":     "",
		"ldap_bind_password": "",
		"ldap_user_filter": "(uid=%s)",
	}

	insertSQL := `INSERT INTO app_settings (key, value, description) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO NOTHING`

	for k, v := range settings {
		var desc string
		switch k {
		case "retention_days":
			desc = "Days to keep logs before auto-deletion"
		case "default_role":
			desc = "Default role for new users"
		case "jwt_expiry":
			desc = "JWT token expiry in hours"
		case "is_initialized":
			desc = "Application initialization flag"
		case "ldap_enabled":
			desc = "Enable LDAP authentication"
		case "ldap_server":
			desc = "LDAP server hostname"
		case "ldap_port":
			desc = "LDAP server port"
		case "ldap_use_tls":
			desc = "Use TLS for LDAP connection"
		case "ldap_base_dn":
			desc = "LDAP base DN for searches"
		case "ldap_bind_dn":
			desc = "LDAP bind DN for service account"
		case "ldap_bind_password":
			desc = "LDAP bind password for service account"
		case "ldap_user_filter":
			desc = "LDAP user search filter (%s = username)"
		}
		if _, err := db.Exec(insertSQL, k, v, desc); err != nil {
			return fmt.Errorf("seed setting %s: %w", k, err)
		}
	}

	return nil
}

// GetSetting retrieves a setting value, falling back to defaultValue if not found
func GetSetting(db *sql.DB, key, defaultValue string) string {
	var val string
	err := db.QueryRow("SELECT value FROM app_settings WHERE key = $1", key).Scan(&val)
	if err != nil {
		return defaultValue
	}
	return val
}

// UpdateSetting updates a setting value
func UpdateSetting(db *sql.DB, key, value string) error {
	_, err := db.Exec(`UPDATE app_settings SET value = $1 WHERE key = $2
		ON CONFLICT (key) DO UPDATE SET value = $1`, value, key)
	return err
}

// GetAllSettings retrieves all settings as a map
func GetAllSettings(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query("SELECT key, value FROM app_settings ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		settings[k] = v
	}
	return settings, rows.Err()
}

// CleanupOldLogs deletes logs older than retentionDays
func CleanupOldLogs(db *sql.DB, retentionDays int) (int64, error) {
	result, err := db.Exec("DELETE FROM syslog_logs WHERE timestamp < NOW() - INTERVAL $1", fmt.Sprintf("%d days", retentionDays))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	IsAdmin   bool      `json:"is_admin"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

func GetAllUsers(db *sql.DB) ([]User, error) {
	rows, err := db.Query("SELECT id, username, role, is_admin, is_active, created_at FROM users ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.IsAdmin, &u.IsActive, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func CreateUser(db *sql.DB, username, passwordHash string, isAdmin bool, role string) (*User, error) {
	var id int64
	var createdAt time.Time
	err := db.QueryRow(
		"INSERT INTO users (username, password_hash, is_admin, role, is_active) VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at",
		username, passwordHash, isAdmin, role, true,
	).Scan(&id, &createdAt)
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Username: username, Role: role, IsAdmin: isAdmin, IsActive: true, CreatedAt: createdAt}, nil
}

func UpdateUser(db *sql.DB, id int64, role *string, isActive *bool) (*User, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if role != nil {
		isAdmin := *role == "admin"
		if _, err := tx.Exec("UPDATE users SET role = $1, is_admin = $2 WHERE id = $3", *role, isAdmin, id); err != nil {
			return nil, err
		}
	}
	if isActive != nil {
		if _, err := tx.Exec("UPDATE users SET is_active = $1 WHERE id = $2", *isActive, id); err != nil {
			return nil, err
		}
	}

	var u User
	if err := tx.QueryRow("SELECT id, username, role, is_admin, is_active, created_at FROM users WHERE id = $1", id).
		Scan(&u.ID, &u.Username, &u.Role, &u.IsAdmin, &u.IsActive, &u.CreatedAt); err != nil {
		return nil, err
	}

	return &u, tx.Commit()
}

func DeleteUser(db *sql.DB, id int64) error {
	_, err := db.Exec("DELETE FROM users WHERE id = $1 AND id != (SELECT id FROM users WHERE role = 'admin' LIMIT 1)", id)
	return err
}

func ResetUserPassword(db *sql.DB, id int64, passwordHash string) error {
	_, err := db.Exec("UPDATE users SET password_hash = $1 WHERE id = $2", passwordHash, id)
	return err
}
