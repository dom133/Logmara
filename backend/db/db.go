package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"

	"syslog-gui/db/parsers"
	"syslog-gui/util"
)

func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open db: %w", err)
	}

	maxOpen := 100
	maxIdle := 25
	maxLifeTime := 30 * time.Minute
	maxIdleTime := 5 * time.Minute

	if v := os.Getenv("DB_MAX_OPEN_CONNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			maxOpen = n
		}
	}
	if v := os.Getenv("DB_MAX_IDLE_CONNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			maxIdle = n
		}
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(maxLifeTime)
	db.SetConnMaxIdleTime(maxIdleTime)

	slog.Info("db pool configured", "max_open", maxOpen, "max_idle", maxIdle, "max_lifetime", maxLifeTime, "max_idle_time", maxIdleTime)

	for i := 0; i < 5; i++ {
		if err := db.Ping(); err == nil {
			return db, nil
		}
		slog.Warn("waiting for database", "attempt", i+1)
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
			fromhost_ip VARCHAR(255),
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
		`CREATE INDEX IF NOT EXISTS idx_syslog_hostname ON syslog_logs (hostname)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_severity ON syslog_logs (severity)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_app_name ON syslog_logs (app_name)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_composite ON syslog_logs (timestamp DESC, severity, hostname)`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='syslog_logs' AND column_name='parsed_fields') THEN ALTER TABLE syslog_logs ADD COLUMN parsed_fields JSONB DEFAULT '{}'; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='syslog_logs' AND column_name='matched_parsers') THEN ALTER TABLE syslog_logs ADD COLUMN matched_parsers TEXT[] DEFAULT '{}'; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='syslog_logs' AND column_name='fromhost_ip') THEN ALTER TABLE syslog_logs ADD COLUMN fromhost_ip VARCHAR(255); END IF; END $$`,
		`DO $$ BEGIN EXECUTE 'DROP INDEX IF EXISTS idx_syslog_parsed_fields'; EXCEPTION WHEN OTHERS THEN NULL; END $$`,
		`DO $$ BEGIN EXECUTE 'DROP INDEX IF EXISTS idx_syslog_timestamp'; EXCEPTION WHEN OTHERS THEN NULL; END $$`,
		`DO $$ BEGIN EXECUTE 'DROP INDEX IF EXISTS idx_syslog_recent_7d'; EXCEPTION WHEN OTHERS THEN NULL; END $$`,
		`DO $$ BEGIN CREATE INDEX idx_syslog_parsed_fields ON syslog_logs USING GIN (parsed_fields); EXCEPTION WHEN undefined_object THEN NULL; END $$`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_fromhost_ip ON syslog_logs (fromhost_ip)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_fromhost_severity ON syslog_logs (fromhost_ip, severity)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_sev_errors ON syslog_logs (severity, timestamp) WHERE severity IN ('err', 'crit', 'alert', 'emerg')`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_ts_host ON syslog_logs (timestamp DESC, hostname)`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='syslog_logs' AND column_name='search_vector') THEN ALTER TABLE syslog_logs ADD COLUMN search_vector TSVECTOR GENERATED ALWAYS AS (to_tsvector('english', COALESCE(message, '') || ' ' || COALESCE(raw_message, ''))) STORED; END IF; END $$`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_fts ON syslog_logs USING GIN (search_vector)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_dev_ts ON syslog_logs (fromhost_ip, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_sev_ts ON syslog_logs (severity, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_ts_sev_host_cover ON syslog_logs (timestamp DESC) INCLUDE (severity, hostname)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_ts_dev_cover ON syslog_logs (timestamp DESC) INCLUDE (fromhost_ip, hostname)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_dev_sev_cover ON syslog_logs (fromhost_ip) INCLUDE (hostname)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_sev_dev_cover ON syslog_logs (severity) INCLUDE (fromhost_ip, hostname)`,
		`CREATE MATERIALIZED VIEW IF NOT EXISTS mv_dashboard_summary AS
			SELECT
				NOW() as refreshed_at,
				COUNT(*) as total_logs,
				COUNT(*) FILTER (WHERE timestamp >= NOW() - INTERVAL '1 hour') as logs_last_hour,
				COUNT(*) FILTER (WHERE timestamp >= NOW() - INTERVAL '1 day') as logs_last_day,
				COUNT(DISTINCT hostname) as unique_devices,
				COUNT(DISTINCT fromhost_ip) as unique_ips
			FROM syslog_logs
		`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_mv_dashboard_summary_key ON mv_dashboard_summary (refreshed_at)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_coalesce_fromhost_ip ON syslog_logs (COALESCE(fromhost_ip, ''))`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_app_ts_cover ON syslog_logs (app_name, timestamp DESC) INCLUDE (hostname, severity)`,
		`CREATE MATERIALIZED VIEW IF NOT EXISTS mv_dashboard_severity AS
			SELECT NOW() as refreshed_at, severity, COUNT(*) as cnt FROM syslog_logs GROUP BY severity
		`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_mv_dashboard_severity_key ON mv_dashboard_severity (severity)`,
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
		`DO $$ BEGIN IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='dashboards' AND column_name='share_token') THEN ALTER TABLE dashboards DROP COLUMN share_token; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='dashboards' AND column_name='is_public') THEN ALTER TABLE dashboards ADD COLUMN is_public BOOLEAN DEFAULT FALSE; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name='role') THEN ALTER TABLE users ADD COLUMN role VARCHAR(50) DEFAULT 'viewer'; END IF; END $$`,
		`UPDATE users SET role = 'admin' WHERE is_admin = TRUE AND role = 'viewer'`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name='is_active') THEN ALTER TABLE users ADD COLUMN is_active BOOLEAN DEFAULT TRUE; END IF; END $$`,
		`CREATE TABLE IF NOT EXISTS user_dashboard_pins (
			user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
			dashboard_id INTEGER REFERENCES dashboards(id) ON DELETE CASCADE,
			PRIMARY KEY (user_id, dashboard_id)
		)`,
		`INSERT INTO user_dashboard_pins (user_id, dashboard_id) SELECT owner_id, id FROM dashboards WHERE pinned = TRUE ON CONFLICT DO NOTHING`,
		`DO $$ BEGIN IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='dashboards' AND column_name='pinned') THEN ALTER TABLE dashboards DROP COLUMN pinned; END IF; END $$`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key VARCHAR(100) PRIMARY KEY,
			value TEXT NOT NULL,
			description TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS refresh_tokens (
			id SERIAL PRIMARY KEY,
			user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
			token VARCHAR(255) UNIQUE NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			used BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id SERIAL PRIMARY KEY,
			user_id INTEGER,
			username VARCHAR(100),
			action VARCHAR(100) NOT NULL,
			ip VARCHAR(100),
			details TEXT,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name='email') THEN ALTER TABLE users ADD COLUMN email VARCHAR(255) DEFAULT ''; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name='last_login_at') THEN ALTER TABLE users ADD COLUMN last_login_at TIMESTAMPTZ; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name='auth_type') THEN ALTER TABLE users ADD COLUMN auth_type VARCHAR(20) DEFAULT 'local'; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='dashboards' AND column_name='updated_by') THEN ALTER TABLE dashboards ADD COLUMN updated_by INTEGER REFERENCES users(id); END IF; END $$`,
		`UPDATE dashboards SET updated_by = owner_id WHERE updated_by IS NULL`,
		`CREATE TABLE IF NOT EXISTS device_aliases (
			fromhost_ip VARCHAR(255) PRIMARY KEY,
			display_name VARCHAR(255) NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='device_aliases' AND column_name='old_hostname') THEN ALTER TABLE device_aliases ADD COLUMN old_hostname VARCHAR(255); END IF; END $$`,
		`DO $$
DECLARE
	v_min_ts TIMESTAMPTZ;
	v_max_ts TIMESTAMPTZ;
	v_start DATE;
	v_end DATE;
	v_curr DATE;
	v_part_name TEXT;
BEGIN
	-- Skip if already partitioned
	IF EXISTS (SELECT 1 FROM pg_class WHERE relname = 'syslog_logs' AND relkind = 'p') THEN
		RETURN;
	END IF;

	SELECT MIN(timestamp), MAX(timestamp) INTO v_min_ts, v_max_ts FROM syslog_logs;

	IF v_min_ts IS NOT NULL THEN
		v_start := date_trunc('month', v_min_ts)::DATE;
		v_end := (date_trunc('month', v_max_ts) + INTERVAL '1 month')::DATE;
	ELSE
		v_start := date_trunc('month', NOW())::DATE;
		v_end := (date_trunc('month', NOW()) + INTERVAL '1 month')::DATE;
	END IF;

	ALTER TABLE syslog_logs DROP CONSTRAINT IF EXISTS syslog_logs_pkey;
	ALTER TABLE syslog_logs ADD PRIMARY KEY (timestamp, id);
	ALTER TABLE syslog_logs SET PARTITION KEY (RANGE (timestamp));

	v_curr := v_start;
	WHILE v_curr < v_end LOOP
		v_part_name := 'syslog_logs_' || to_char(v_curr, 'YYYY_MM');
		EXECUTE format(
			'CREATE TABLE IF NOT EXISTS %I PARTITION OF syslog_logs FOR VALUES FROM (%L) TO (%L)',
			v_part_name, v_curr, v_curr + INTERVAL '1 month'
		);
		v_curr := v_curr + INTERVAL '1 month';
	END LOOP;

	EXECUTE 'CREATE TABLE IF NOT EXISTS syslog_logs_default PARTITION OF syslog_logs DEFAULT';
END $$`,
		`DO $$ BEGIN CREATE INDEX idx_syslog_timestamp ON syslog_logs USING BRIN (timestamp); EXCEPTION WHEN duplicate_object THEN NULL; WHEN undefined_object THEN NULL; END $$`,
		`DO $$ BEGIN CREATE INDEX idx_syslog_recent_7d ON syslog_logs (timestamp DESC) WHERE timestamp >= NOW() - INTERVAL '7 days'; EXCEPTION WHEN duplicate_object THEN NULL; WHEN undefined_object THEN NULL; END $$`,
		`CREATE MATERIALIZED VIEW IF NOT EXISTS mv_timeline_hourly AS
			SELECT date_trunc('hour', timestamp) AS hour, COUNT(*) AS cnt FROM syslog_logs GROUP BY 1 ORDER BY 1
		`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_mv_timeline_hourly_key ON mv_timeline_hourly (hour)`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migration failed (%s): %w", stmt[:50], err)
		}
	}

	if err := seedParsers(db); err != nil {
		slog.Warn("seeding parsers failed", "error", err)
	}

	if err := seedSettings(db); err != nil {
		slog.Warn("seeding settings failed", "error", err)
	}

	slog.Info("database migration completed")
	return nil
}

func seedParsers(db *sql.DB) error {
	allParsers := parsers.AllParsers

	rows, err := db.Query("SELECT id, name FROM parsers WHERE is_builtin")
	if err != nil {
		return fmt.Errorf("query builtin parsers: %w", err)
	}
	dbByName := make(map[string]int64)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return err
		}
		dbByName[name] = id
	}
	rows.Close()

	codeNames := make(map[string]bool)
	for _, p := range allParsers {
		codeNames[p.Name] = true
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	insertParser := `INSERT INTO parsers (name, description, device_type, match_type, match_value, regex, enabled, is_builtin)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`
	updateParser := `UPDATE parsers SET description=$2, device_type=$3, match_type=$4, match_value=$5, regex=$6, enabled=$7
		WHERE id=$1`
	deleteFields := `DELETE FROM parsed_fields_registry WHERE parser_id=$1`
	insertField := `INSERT INTO parsed_fields_registry (parser_id, field_name, field_label, field_type)
		VALUES ($1, $2, $3, $4)`

	for _, p := range allParsers {
		var parserID int64
		desc := nullStrPtr(p.Description)
		matchVal := nullStrPtr(p.MatchValue)

		existingID, exists := dbByName[p.Name]
		if exists {
			parserID = existingID
			if _, err := tx.Exec(updateParser, parserID, desc, p.DeviceType, p.MatchType, matchVal, p.Regex, true); err != nil {
				return fmt.Errorf("update parser %s: %w", p.Name, err)
			}
			if _, err := tx.Exec(deleteFields, parserID); err != nil {
				return fmt.Errorf("clear fields for parser %s: %w", p.Name, err)
			}
		} else {
			if err := tx.QueryRow(insertParser, p.Name, desc, p.DeviceType, p.MatchType, matchVal, p.Regex, true, true).
				Scan(&parserID); err != nil {
				return fmt.Errorf("seed parser %s: %w", p.Name, err)
			}
		}

		for _, f := range p.Fields {
			if _, err := tx.Exec(insertField, parserID, f.Name, f.Label, f.Type); err != nil {
				return fmt.Errorf("seed field %s for parser %s: %w", f.Name, p.Name, err)
			}
		}
	}

	for name, id := range dbByName {
		if !codeNames[name] {
			if _, err := tx.Exec("DELETE FROM parsed_fields_registry WHERE parser_id=$1", id); err != nil {
				return fmt.Errorf("clear orphan fields: %w", err)
			}
			if _, err := tx.Exec("DELETE FROM parsers WHERE id=$1", id); err != nil {
				return fmt.Errorf("remove orphan parser %s: %w", name, err)
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
		"retention_days":      "30",
		"jwt_expiry":          "24",
		"is_initialized":      "false",
		"ldap_enabled":        "false",
		"ldap_server":         "",
		"ldap_port":           "389",
		"ldap_use_tls":        "false",
		"ldap_verify_cert":    "true",
		"ldap_ca_cert":        "",
		"ldap_base_dn":        "",
		"ldap_bind_dn":        "",
		"ldap_bind_password":  "",
		"ldap_user_filter":    "(uid=%s)",
		"ldap_username_attr":  "uid",
		"ldap_email_attr":     "mail",
		"ldap_default_role":   "viewer",
		"ldap_auto_provision": "true",
		"encryption_key":      "",
		"cors_origins":        "",
	}

	insertSQL := `INSERT INTO app_settings (key, value, description) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO NOTHING`

	for k, v := range settings {
		var desc string
		switch k {
		case "retention_days":
			desc = "Days to keep logs before auto-deletion"
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
		case "ldap_username_attr":
			desc = "LDAP attribute mapped to username"
		case "ldap_email_attr":
			desc = "LDAP attribute mapped to email"
		case "ldap_default_role":
			desc = "Default role for auto-provisioned LDAP users"
		case "ldap_auto_provision":
			desc = "Auto-create local account for LDAP users"
		case "ldap_verify_cert":
			desc = "Verify LDAP server TLS certificate"
		case "ldap_ca_cert":
			desc = "Custom CA certificate for LDAP TLS (PEM format)"
		case "encryption_key":
			desc = "AES-256 encryption key for sensitive data"
		case "cors_origins":
			desc = "Allowed CORS origins (comma-separated)"
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
	if key == "ldap_bind_password" && val != "" {
		encKey := os.Getenv("ENCRYPTION_KEY")
		if encKey == "" {
			encKey = getSettingRaw(db, "encryption_key")
		}
		if encKey != "" {
			decrypted, err := util.Decrypt(encKey, val)
			if err == nil {
				return decrypted
			}
		}
	}
	return val
}

// UpdateSetting updates a setting value, encrypting sensitive fields
func UpdateSetting(db *sql.DB, key, value string) error {
	if key == "ldap_bind_password" && value != "" {
		encKey := os.Getenv("ENCRYPTION_KEY")
		if encKey == "" {
			encKey = getSettingRaw(db, "encryption_key")
		}
		if encKey != "" {
			encrypted, err := util.Encrypt(encKey, value)
			if err == nil {
				value = encrypted
			}
		}
	}
	_, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = $2`, key, value)
	return err
}

func getSettingRaw(db *sql.DB, key string) string {
	var val string
	err := db.QueryRow("SELECT value FROM app_settings WHERE key = $1", key).Scan(&val)
	if err != nil {
		return ""
	}
	return val
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
	const batchSize = 10000
	var totalDeleted int64

	for {
		result, err := db.Exec(
			"DELETE FROM syslog_logs WHERE ctid IN (SELECT ctid FROM syslog_logs WHERE timestamp < NOW() - INTERVAL $1 LIMIT $2)",
			fmt.Sprintf("%d days", retentionDays), batchSize,
		)
		if err != nil {
			return totalDeleted, err
		}
		affected, _ := result.RowsAffected()
		totalDeleted += affected
		if affected == 0 {
			break
		}
	}
	return totalDeleted, nil
}

type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"-"`
	Role         string     `json:"role"`
	AuthType     string     `json:"auth_type"`
	IsAdmin      bool       `json:"is_admin"`
	IsActive     bool       `json:"is_active"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at"`
}

type AuditLog struct {
	ID        int64     `json:"id"`
	UserID    *int64    `json:"user_id"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	IP        *string   `json:"ip"`
	Details   *string   `json:"details"`
	CreatedAt time.Time `json:"created_at"`
}

func GetAllUsers(db *sql.DB) ([]User, error) {
	rows, err := db.Query("SELECT id, username, email, role, auth_type, is_admin, is_active, created_at, last_login_at FROM users ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.AuthType, &u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.LastLoginAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func CreateUser(db *sql.DB, username, passwordHash, email string, isAdmin bool, role string) (*User, error) {
	var id int64
	var createdAt time.Time
	err := db.QueryRow(
		"INSERT INTO users (username, password_hash, email, is_admin, role, is_active, auth_type) VALUES ($1, $2, $3, $4, $5, $6, 'local') RETURNING id, created_at",
		username, passwordHash, email, isAdmin, role, true,
	).Scan(&id, &createdAt)
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Username: username, Email: email, Role: role, IsAdmin: isAdmin, IsActive: true, CreatedAt: createdAt}, nil
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
	if err := tx.QueryRow("SELECT id, username, email, role, is_admin, is_active, created_at, last_login_at FROM users WHERE id = $1", id).
		Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.LastLoginAt); err != nil {
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

func CreateLDAPUser(db *sql.DB, username, email, role string, isAdmin bool) (*User, error) {
	var id int64
	var createdAt time.Time
	err := db.QueryRow(
		"INSERT INTO users (username, password_hash, email, is_admin, role, is_active, auth_type) VALUES ($1, '', $2, $3, $4, $5, 'ldap') RETURNING id, created_at",
		username, email, isAdmin, role, true,
	).Scan(&id, &createdAt)
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Username: username, Email: email, Role: role, AuthType: "ldap", IsAdmin: isAdmin, IsActive: true, CreatedAt: createdAt}, nil
}

func UpdateLastLogin(db *sql.DB, username string) error {
	_, err := db.Exec("UPDATE users SET last_login_at = NOW() WHERE username = $1", username)
	return err
}

func GetUserByUsername(db *sql.DB, username string) (*User, error) {
	var u User
	err := db.QueryRow(
		"SELECT id, username, email, password_hash, role, is_admin, is_active, created_at, last_login_at FROM users WHERE username = $1",
		username,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func RefreshMaterializedViews(db *sql.DB) {
	slog.Info("refreshing materialized views")
	_, err1 := db.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY mv_dashboard_summary")
	_, err2 := db.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY mv_dashboard_severity")
	if err1 != nil || err2 != nil {
		slog.Error("materialized view refresh failed", "err1", err1, "err2", err2)
	}
	slog.Info("materialized views refreshed")
}

func StartMVRefreshLoop(db *sql.DB, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			RefreshMaterializedViews(db)
		}
	}()
	slog.Info("materialized view refresh loop started", "interval", interval)
}
