package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/lib/pq"

	"logmara/db/parsers"
	"logmara/util"
)

var appStarting atomic.Bool

func SetAppStarting(v bool) {
	appStarting.Store(v)
}

func IsAppStarting() bool {
	return appStarting.Load()
}

func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres-instrumented", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open db: %w", err)
	}

	maxOpen := 500
	maxIdle := 100
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

	var lastErr error
	for i := 0; i < 5; i++ {
		if err := db.Ping(); err == nil {
			return db, nil
		} else {
			lastErr = err
		}
		slog.Warn("waiting for database", "attempt", i+1, "error", lastErr)
		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("could not connect to database after 5 attempts: %w", lastErr)
}

// migrationLockKey is an arbitrary constant used as a Postgres advisory
// lock key, scoping the lock to "this application's migration" so it can't
// collide with an advisory lock taken by something unrelated sharing the
// same database.
const migrationLockKey = 8743011

// MigrateWithLock runs Migrate() under a session-level Postgres advisory
// lock, so that multiple api replicas starting concurrently (normal during
// a rolling Swarm deploy or a simple restart race) serialize against each
// other instead of racing on schema DDL - in particular the one-time
// partitioning migration inside Migrate, which drops and recreates
// syslog_logs and is not safe to run twice concurrently. pg_advisory_lock
// blocks until acquired rather than failing, so a losing replica simply
// waits its turn; once it acquires the lock, Migrate's own
// CREATE-IF-NOT-EXISTS/guarded-ALTER statements make its run a fast no-op.
func MigrateWithLock(db *sql.DB) error {
	ctx := context.Background()

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for migration lock: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrationLockKey); err != nil {
			slog.Warn("failed to release migration advisory lock", "error", err)
		}
	}()

	return Migrate(db)
}

// schemaVersion must be bumped whenever a statement is appended to
// statements/partitionStmts/postStmts below. Migrate short-circuits once the
// schema_version table already records this value, so a forgotten bump
// means an already-deployed instance will never see the new statement
// applied.
const schemaVersion = 6

func ensureSchemaVersionTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	return err
}

func getSchemaVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow(`SELECT version FROM schema_version ORDER BY version DESC LIMIT 1`).Scan(&version)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return version, err
}

func setSchemaVersion(db *sql.DB, version int) error {
	if _, err := db.Exec(`DELETE FROM schema_version`); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO schema_version (version) VALUES ($1)`, version)
	return err
}

func Migrate(db *sql.DB) error {
	if err := ensureSchemaVersionTable(db); err != nil {
		return fmt.Errorf("ensure schema_version table: %w", err)
	}
	current, err := getSchemaVersion(db)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if current >= schemaVersion {
		slog.Info("schema up to date, skipping DDL migration", "version", current)
	} else {
		if err := runSchemaMigration(db); err != nil {
			return err
		}
	}

	// Builtin parser/setting definitions (e.g. PARSER_DEFS_DIR contents) can
	// change independently of the schema DDL above, so these must always
	// re-sync on every start rather than being gated on schemaVersion - a
	// gate here would mean an already-migrated instance never picks up a
	// newly added/edited builtin parser.
	if err := seedParsers(db); err != nil {
		slog.Warn("seeding parsers failed", "error", err)
	}

	if err := seedSettings(db); err != nil {
		slog.Warn("seeding settings failed", "error", err)
	}

	return nil
}

func runSchemaMigration(db *sql.DB) error {
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
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='syslog_logs' AND column_name='parsed_fields') THEN ALTER TABLE syslog_logs ADD COLUMN parsed_fields JSONB DEFAULT '{}'; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='syslog_logs' AND column_name='matched_parsers') THEN ALTER TABLE syslog_logs ADD COLUMN matched_parsers TEXT[] DEFAULT '{}'; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='syslog_logs' AND column_name='fromhost_ip') THEN ALTER TABLE syslog_logs ADD COLUMN fromhost_ip VARCHAR(255); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='syslog_logs' AND column_name='via_relay') THEN ALTER TABLE syslog_logs ADD COLUMN via_relay VARCHAR(255); END IF; END $$`,
		`DO $$ BEGIN EXECUTE 'DROP INDEX IF EXISTS idx_syslog_parsed_fields'; EXCEPTION WHEN OTHERS THEN NULL; END $$`,
		`DO $$ BEGIN EXECUTE 'DROP INDEX IF EXISTS idx_syslog_timestamp'; EXCEPTION WHEN OTHERS THEN NULL; END $$`,
		`DO $$ BEGIN EXECUTE 'DROP INDEX IF EXISTS idx_syslog_recent_7d'; EXCEPTION WHEN OTHERS THEN NULL; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='syslog_logs' AND column_name='search_vector') THEN ALTER TABLE syslog_logs ADD COLUMN search_vector TSVECTOR GENERATED ALWAYS AS (to_tsvector('english', COALESCE(message, '') || ' ' || COALESCE(raw_message, ''))) STORED; END IF; END $$`,
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
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name='failed_login_attempts') THEN ALTER TABLE users ADD COLUMN failed_login_attempts INTEGER DEFAULT 0; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name='locked_until') THEN ALTER TABLE users ADD COLUMN locked_until TIMESTAMPTZ; END IF; END $$`,
		`CREATE TABLE IF NOT EXISTS jwt_blacklist (
			jti TEXT PRIMARY KEY,
			blacklisted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
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
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='used_at') THEN ALTER TABLE refresh_tokens ADD COLUMN used_at TIMESTAMPTZ; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='replaced_by') THEN ALTER TABLE refresh_tokens ADD COLUMN replaced_by VARCHAR(255); END IF; END $$`,
		// Per-device "remember me" support: device_id identifies the browser
		// (a long-lived cookie set on first login), remember marks the token
		// as exempt from the inactivity-based expiry in maintenance.go, and
		// user_agent/ip/last_used_at back the "active sessions" self-service
		// list/revoke endpoints.
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='device_id') THEN ALTER TABLE refresh_tokens ADD COLUMN device_id VARCHAR(64); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='user_agent') THEN ALTER TABLE refresh_tokens ADD COLUMN user_agent VARCHAR(500); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='ip') THEN ALTER TABLE refresh_tokens ADD COLUMN ip VARCHAR(100); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='remember') THEN ALTER TABLE refresh_tokens ADD COLUMN remember BOOLEAN NOT NULL DEFAULT FALSE; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='last_used_at') THEN ALTER TABLE refresh_tokens ADD COLUMN last_used_at TIMESTAMPTZ; END IF; END $$`,
		// jti links a refresh_tokens row to the access token issued alongside
		// it, so revoking a session (Admin/self "Sign out" on another device)
		// can also blacklist that still-live access token instead of leaving
		// it valid until its own natural expiry - see handler.RevokeSession
		// and GET /auth/session-check, which the frontend polls precisely to
		// notice that blacklisting quickly rather than on its own schedule.
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='jti') THEN ALTER TABLE refresh_tokens ADD COLUMN jti VARCHAR(64); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='token_hash') THEN ALTER TABLE refresh_tokens ADD COLUMN token_hash VARCHAR(64); END IF; END $$`,
		// Screen resolution and timezone fingerprint for session identification
		// in the "active sessions" list, helping users verify if a session
		// belongs to their device.
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='screen_resolution') THEN ALTER TABLE refresh_tokens ADD COLUMN screen_resolution VARCHAR(20); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='refresh_tokens' AND column_name='timezone') THEN ALTER TABLE refresh_tokens ADD COLUMN timezone VARCHAR(50); END IF; END $$`,
		`CREATE TABLE IF NOT EXISTS password_history (
			id BIGSERIAL PRIMARY KEY,
			user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
			password_hash VARCHAR(255) NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`DELETE FROM app_settings WHERE key = 'jwt_expiry'`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='rule_type') THEN ALTER TABLE alerts ADD COLUMN rule_type VARCHAR(30) NOT NULL DEFAULT 'log_threshold'; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='severity') THEN ALTER TABLE alerts ADD COLUMN severity VARCHAR(20); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='hostname_pattern') THEN ALTER TABLE alerts ADD COLUMN hostname_pattern VARCHAR(255); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='app_name_pattern') THEN ALTER TABLE alerts ADD COLUMN app_name_pattern VARCHAR(255); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='message_pattern') THEN ALTER TABLE alerts ADD COLUMN message_pattern VARCHAR(500); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='cooldown_minutes') THEN ALTER TABLE alerts ADD COLUMN cooldown_minutes INTEGER NOT NULL DEFAULT 15; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='audit_action_filter') THEN ALTER TABLE alerts ADD COLUMN audit_action_filter VARCHAR(100); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='created_by') THEN ALTER TABLE alerts ADD COLUMN created_by INTEGER REFERENCES users(id) ON DELETE SET NULL; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='updated_at') THEN ALTER TABLE alerts ADD COLUMN updated_at TIMESTAMPTZ DEFAULT NOW(); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='last_fired_at') THEN ALTER TABLE alerts ADD COLUMN last_fired_at TIMESTAMPTZ; END IF; END $$`,
		`ALTER TABLE alerts ALTER COLUMN condition DROP NOT NULL`,
		`ALTER TABLE alerts ALTER COLUMN threshold DROP NOT NULL`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='device_ips') THEN ALTER TABLE alerts ADD COLUMN device_ips TEXT[]; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='parser_names') THEN ALTER TABLE alerts ADD COLUMN parser_names TEXT[]; END IF; END $$`,
		`CREATE TABLE IF NOT EXISTS alert_field_conditions (
			id SERIAL PRIMARY KEY,
			alert_id INTEGER REFERENCES alerts(id) ON DELETE CASCADE,
			field_name VARCHAR(100) NOT NULL,
			operator VARCHAR(20) NOT NULL DEFAULT 'equals',
			value VARCHAR(500) NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_alert_field_conditions_alert ON alert_field_conditions (alert_id)`,
		// Tracks which (device_silence rule, device) pairs are currently
		// silent, so alertengine.CheckDeviceSilence can detect the
		// silent->recovered transition (to send a "back online" notice) and
		// escalate severity the longer a device stays silent - see silence.go.
		`CREATE TABLE IF NOT EXISTS device_silence_state (
			rule_id INTEGER REFERENCES alerts(id) ON DELETE CASCADE,
			device_ip VARCHAR(64) NOT NULL,
			silent_since TIMESTAMPTZ NOT NULL,
			last_severity VARCHAR(20) NOT NULL DEFAULT 'warning',
			PRIMARY KEY (rule_id, device_ip)
		)`,
		`CREATE TABLE IF NOT EXISTS notification_channels (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			type VARCHAR(20) NOT NULL,
			config JSONB NOT NULL DEFAULT '{}',
			secret TEXT,
			enabled BOOLEAN DEFAULT TRUE,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='notification_channels' AND column_name='created_by') THEN ALTER TABLE notification_channels ADD COLUMN created_by INTEGER REFERENCES users(id) ON DELETE SET NULL; END IF; END $$`,
		`CREATE TABLE IF NOT EXISTS alert_channels (
			alert_id INTEGER REFERENCES alerts(id) ON DELETE CASCADE,
			channel_id INTEGER REFERENCES notification_channels(id) ON DELETE CASCADE,
			PRIMARY KEY (alert_id, channel_id)
		)`,
		`CREATE TABLE IF NOT EXISTS notification_log (
			id BIGSERIAL PRIMARY KEY,
			alert_id INTEGER REFERENCES alerts(id) ON DELETE SET NULL,
			alert_name VARCHAR(255),
			channel_id INTEGER REFERENCES notification_channels(id) ON DELETE SET NULL,
			channel_name VARCHAR(255),
			channel_type VARCHAR(20),
			status VARCHAR(20) NOT NULL,
			detail TEXT,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notification_log_created ON notification_log (created_at DESC)`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='notification_log' AND column_name='trigger_log') THEN ALTER TABLE notification_log ADD COLUMN trigger_log JSONB; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='notification_log' AND column_name='matched_conditions') THEN ALTER TABLE notification_log ADD COLUMN matched_conditions JSONB; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='notification_log' AND column_name='in_app_notification_id') THEN ALTER TABLE notification_log ADD COLUMN in_app_notification_id BIGINT; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='notification_log' AND column_name='firing_id') THEN ALTER TABLE notification_log ADD COLUMN firing_id VARCHAR(36); END IF; END $$`,
		`CREATE INDEX IF NOT EXISTS idx_notification_log_firing_id ON notification_log (firing_id)`,
		`CREATE TABLE IF NOT EXISTS in_app_notifications (
			id BIGSERIAL PRIMARY KEY,
			alert_id INTEGER REFERENCES alerts(id) ON DELETE SET NULL,
			title VARCHAR(255) NOT NULL,
			message TEXT NOT NULL,
			severity VARCHAR(20) DEFAULT 'info',
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_in_app_notifications_created ON in_app_notifications (created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS user_notification_state (
			user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			last_read_id BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS push_subscriptions (
			id SERIAL PRIMARY KEY,
			user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
			endpoint TEXT NOT NULL UNIQUE,
			p256dh TEXT NOT NULL,
			auth TEXT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_push_subscriptions_user ON push_subscriptions (user_id)`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='notification_log' AND column_name='audit_log_ref') THEN ALTER TABLE notification_log ADD COLUMN audit_log_ref JSONB; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='notification_log' AND column_name='rule_type') THEN ALTER TABLE notification_log ADD COLUMN rule_type VARCHAR(50) DEFAULT ''; END IF; END $$`,
		`UPDATE alerts SET rule_type = 'audit_log' WHERE rule_type = 'config_change'`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='fire_on_every_match') THEN ALTER TABLE alerts ADD COLUMN fire_on_every_match BOOLEAN NOT NULL DEFAULT FALSE; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='alerts' AND column_name='field_conditions_logic') THEN ALTER TABLE alerts ADD COLUMN field_conditions_logic VARCHAR(10) NOT NULL DEFAULT 'and'; END IF; END $$`,
		`CREATE TABLE IF NOT EXISTS relay_certificates (
			id SERIAL PRIMARY KEY,
			label VARCHAR(255) NOT NULL,
			serial_hex VARCHAR(64) NOT NULL,
			fingerprint_sha256 VARCHAR(64) NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'issued',
			issued_at TIMESTAMPTZ DEFAULT NOW(),
			issued_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
			revoked_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS relay_whitelist (
			id SERIAL PRIMARY KEY,
			ip_address VARCHAR(64) UNIQUE NOT NULL,
			label VARCHAR(255) NOT NULL,
			relay_cert_id INTEGER REFERENCES relay_certificates(id) ON DELETE SET NULL,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			created_by INTEGER REFERENCES users(id) ON DELETE SET NULL
		)`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='relay_certificates' AND column_name='expires_at') THEN ALTER TABLE relay_certificates ADD COLUMN expires_at TIMESTAMPTZ; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='in_app_notifications' AND column_name='alert_rule_type') THEN ALTER TABLE in_app_notifications ADD COLUMN alert_rule_type VARCHAR(50) DEFAULT ''; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='in_app_notifications' AND column_name='target_user_ids') THEN ALTER TABLE in_app_notifications ADD COLUMN target_user_ids INT[]; END IF; END $$`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			key_hash VARCHAR(128) NOT NULL UNIQUE,
			key_prefix VARCHAR(8) NOT NULL,
			permissions JSONB NOT NULL DEFAULT '{"export_json":false,"export_parsed":false,"view_stats":false}',
			scope_filters JSONB DEFAULT NULL,
			is_active BOOLEAN DEFAULT TRUE,
			rate_limit_per_min INTEGER DEFAULT 60,
			expires_at TIMESTAMPTZ DEFAULT NULL,
			last_used_at TIMESTAMPTZ DEFAULT NULL,
			total_requests BIGINT DEFAULT 0,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			created_by INTEGER REFERENCES users(id) ON DELETE SET NULL
		)`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='api_keys' AND column_name='allowed_ips') THEN ALTER TABLE api_keys ADD COLUMN allowed_ips TEXT[]; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name='password_changed_at') THEN ALTER TABLE users ADD COLUMN password_changed_at TIMESTAMPTZ; END IF; END $$`,
		`UPDATE users SET password_changed_at = created_at WHERE password_changed_at IS NULL`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migration failed (%s): %w", stmt[:50], err)
		}
	}

	var isPartitioned bool
	db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_class WHERE relname = 'syslog_logs' AND relkind = 'p')").Scan(&isPartitioned)
	if !isPartitioned {
		partitionStmts := []string{
			`ALTER TABLE syslog_logs DROP CONSTRAINT IF EXISTS syslog_logs_pkey`,
			`ALTER TABLE syslog_logs ADD PRIMARY KEY (timestamp, id)`,
			`DROP TABLE IF EXISTS syslog_logs_new CASCADE`,
			`CREATE TABLE syslog_logs_new (LIKE syslog_logs INCLUDING DEFAULTS INCLUDING GENERATED INCLUDING STORAGE) PARTITION BY RANGE (timestamp)`,
			`ALTER TABLE syslog_logs_new ADD PRIMARY KEY (timestamp, id)`,
			`DO $$
DECLARE
	v_min_ts TIMESTAMPTZ;
	v_max_ts TIMESTAMPTZ;
	v_start DATE;
	v_end DATE;
	v_curr DATE;
BEGIN
	SELECT MIN(timestamp), MAX(timestamp) INTO v_min_ts, v_max_ts FROM syslog_logs;
	IF v_min_ts IS NOT NULL THEN
		v_start := date_trunc('month', v_min_ts)::DATE;
		v_end := (date_trunc('month', v_max_ts) + INTERVAL '1 month')::DATE;
	ELSE
		v_start := date_trunc('month', NOW())::DATE;
		v_end := (date_trunc('month', NOW()) + INTERVAL '1 month')::DATE;
	END IF;
	v_curr := v_start;
	WHILE v_curr < v_end LOOP
		EXECUTE format('CREATE TABLE IF NOT EXISTS %I PARTITION OF syslog_logs_new FOR VALUES FROM (%L) TO (%L)', 'syslog_logs_' || to_char(v_curr, 'YYYY_MM'), v_curr, v_curr + INTERVAL '1 month');
		v_curr := v_curr + INTERVAL '1 month';
	END LOOP;
	EXECUTE 'CREATE TABLE IF NOT EXISTS syslog_logs_default PARTITION OF syslog_logs_new DEFAULT';
END $$`,
			`INSERT INTO syslog_logs_new (id, timestamp, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, created_at, matched_parsers) SELECT id, timestamp, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, created_at, matched_parsers FROM syslog_logs`,
			`DROP TABLE syslog_logs CASCADE`,
			`ALTER TABLE syslog_logs_new RENAME TO syslog_logs`,
			`CREATE SEQUENCE IF NOT EXISTS syslog_logs_id_seq OWNED BY syslog_logs.id`,
			`ALTER TABLE syslog_logs ALTER COLUMN id SET DEFAULT nextval('syslog_logs_id_seq')`,
			`SELECT setval('syslog_logs_id_seq', GREATEST(1, COALESCE((SELECT MAX(id) FROM syslog_logs), 0)))`,
		}
		for _, stmt := range partitionStmts {
			if _, err := db.Exec(stmt); err != nil {
				truncated := stmt
				if len(truncated) > 50 {
					truncated = truncated[:50]
				}
				return fmt.Errorf("partitioning failed (%s): %w", truncated, err)
			}
		}
	}

	// These indexes and materialized views all depend on syslog_logs, so they
	// must be created here, AFTER the (possible) partitioning migration above -
	// not in the main statements list. On a brand new database, the one-time
	// partitioning step replaces syslog_logs via `DROP TABLE syslog_logs
	// CASCADE`, which silently drops any index or materialized view still
	// bound to the original (pre-partition) table object. Creating them
	// against the final, stable table avoids losing them on first deploy.
	postStmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_syslog_hostname ON syslog_logs (hostname)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_severity ON syslog_logs (severity)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_app_name ON syslog_logs (app_name)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_composite ON syslog_logs (timestamp DESC, severity, hostname)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_parsed_fields ON syslog_logs USING GIN (parsed_fields)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_fromhost_ip ON syslog_logs (fromhost_ip)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_fromhost_severity ON syslog_logs (fromhost_ip, severity)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_sev_errors ON syslog_logs (severity, timestamp) WHERE severity IN ('err', 'crit', 'alert', 'emerg')`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_ts_host ON syslog_logs (timestamp DESC, hostname)`,
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
		`CREATE INDEX IF NOT EXISTS idx_syslog_coalesce_dev_ts ON syslog_logs (COALESCE(fromhost_ip, ''), timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_app_ts_cover ON syslog_logs (app_name, timestamp DESC) INCLUDE (hostname, severity)`,
		`CREATE MATERIALIZED VIEW IF NOT EXISTS mv_dashboard_severity AS
			SELECT NOW() as refreshed_at, severity, COUNT(*) as cnt FROM syslog_logs GROUP BY severity
		`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_mv_dashboard_severity_key ON mv_dashboard_severity (severity)`,
		`CREATE MATERIALIZED VIEW IF NOT EXISTS mv_dashboard_top_errors AS
			SELECT NOW() as refreshed_at, LEFT(message, 100) as message,
				COALESCE(fromhost_ip, '') as fromhost_ip, MIN(hostname) as hostname, COUNT(*) as cnt
			FROM syslog_logs
			WHERE severity IN ('err', 'crit', 'alert', 'emerg') AND timestamp >= NOW() - INTERVAL '7 days'
			GROUP BY LEFT(message, 100), fromhost_ip
			ORDER BY cnt DESC
			LIMIT 10
		`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_mv_dashboard_top_errors_key ON mv_dashboard_top_errors (message, fromhost_ip)`,
		`CREATE EXTENSION IF NOT EXISTS pg_trgm`,
		`CREATE INDEX IF NOT EXISTS idx_syslog_app_name_trgm ON syslog_logs USING GIN (app_name gin_trgm_ops)`,
		// mv_device_stats predates the via_relay column: CREATE ... IF NOT
		// EXISTS below is a no-op on any deployment where the view already
		// exists, so an already-materialized copy needs dropping first to
		// pick up the new column (its unique index goes with it, recreated
		// by the CREATE UNIQUE INDEX statement further down). Also drop the
		// older definition that took the most recent *non-blank* via_relay
		// (so a device that had switched from relay to direct still showed
		// "via relay" forever) in favor of the true most-recent-log value.
		`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='mv_device_stats' AND column_name='via_relay')
				-- pg_get_viewdef() heavily re-parenthesizes FILTER's boolean
				-- expression (e.g. "FILTER (WHERE ((via_relay IS NOT NULL) AND
				-- ...))"), so matching the old clause verbatim would never hit.
				-- FILTER appears nowhere else in this view, so its mere presence
				-- is enough to identify the pre-fix definition.
				OR EXISTS (SELECT 1 FROM pg_matviews WHERE matviewname = 'mv_device_stats' AND definition LIKE '%FILTER%') THEN
				DROP MATERIALIZED VIEW IF EXISTS mv_device_stats;
			END IF;
		END $$`,
		`CREATE MATERIALIZED VIEW IF NOT EXISTS mv_device_stats AS
			WITH dev_stats AS (
				SELECT COALESCE(fromhost_ip, '') as fromhost_ip, MIN(hostname) as hostname,
					COUNT(*) as total_logs, MAX(timestamp) as last_seen,
					SUM(CASE WHEN severity = 'emergency' THEN 1 ELSE 0 END) as emergency,
					SUM(CASE WHEN severity = 'alert' THEN 1 ELSE 0 END) as alert,
					SUM(CASE WHEN severity = 'critical' THEN 1 ELSE 0 END) as critical,
					SUM(CASE WHEN severity = 'error' THEN 1 ELSE 0 END) as err_count,
					SUM(CASE WHEN severity = 'warning' THEN 1 ELSE 0 END) as warning,
					SUM(CASE WHEN severity = 'notice' THEN 1 ELSE 0 END) as notice,
					SUM(CASE WHEN severity = 'info' THEN 1 ELSE 0 END) as info,
					SUM(CASE WHEN severity = 'debug' THEN 1 ELSE 0 END) as debug,
					-- via_relay of this device's single most recent log row, set by
					-- rsyslog/syslog.conf's relayAccept ruleset only for entries that
					-- arrived through a relay (see relayConfSnippet's JsonLines
					-- template) - blank/NULL when the last log came straight to the
					-- central listener, so "Proxy" tracks the current source, not
					-- just whichever relay was last seen at some point in the past.
					(array_agg(via_relay ORDER BY timestamp DESC))[1] as via_relay
				FROM syslog_logs
				GROUP BY fromhost_ip
			),
			dev_parsers AS (
				SELECT COALESCE(fromhost_ip, '') as fromhost_ip,
					array_agg(DISTINCT elem) as parsers
				FROM syslog_logs, unnest(matched_parsers) as elem
				WHERE matched_parsers IS NOT NULL AND matched_parsers != '{}'
				GROUP BY fromhost_ip
			)
			SELECT d.fromhost_ip, d.hostname, d.total_logs, d.last_seen,
				d.emergency, d.alert, d.critical, d.err_count, d.warning, d.notice, d.info, d.debug,
				d.via_relay,
				COALESCE(p.parsers, '{}'::TEXT[]) as parsers
			FROM dev_stats d
			LEFT JOIN dev_parsers p ON p.fromhost_ip = d.fromhost_ip
		`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_mv_device_stats_key ON mv_device_stats (fromhost_ip)`,
		`DO $$ BEGIN CREATE INDEX idx_syslog_timestamp ON syslog_logs USING BRIN (timestamp); EXCEPTION WHEN duplicate_object THEN NULL; WHEN undefined_object THEN NULL; END $$`,
		`CREATE MATERIALIZED VIEW IF NOT EXISTS mv_timeline_hourly AS
			SELECT date_trunc('hour', timestamp) AS hour, COUNT(*) AS cnt FROM syslog_logs GROUP BY 1 ORDER BY 1
		`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_mv_timeline_hourly_key ON mv_timeline_hourly (hour)`,
	}
	for _, stmt := range postStmts {
		if _, err := db.Exec(stmt); err != nil {
			truncated := stmt
			if len(truncated) > 50 {
				truncated = truncated[:50]
			}
			return fmt.Errorf("post-migration failed (%s): %w", truncated, err)
		}
	}

	if err := setSchemaVersion(db, schemaVersion); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}

	slog.Info("database migration completed", "version", schemaVersion)
	return nil
}

func seedParsers(db *sql.DB) error {
	allParsers, loadErrs := parsers.LoadAll(os.Getenv("PARSER_DEFS_DIR"))
	for _, e := range loadErrs {
		slog.Warn("skipping malformed builtin parser definition", "error", e)
	}

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

// seedSettings inserts the default app_settings rows on first run (via
// ON CONFLICT DO NOTHING below, so it never overwrites a value once the row
// exists). CORS_ORIGINS seeds the initial "cors_origins" value from the
// environment for this first-run case only; after initialization the value
// lives in the database and is managed from the admin Settings UI, not env.
func seedSettings(db *sql.DB) error {
	settings := map[string]string{
		"retention_days":                "30",
		"session_timeout_min":           "15",
		"session_remembered_max_days":   "60",
		"is_initialized":                "false",
		"default_language":              "en",
		"ldap_enabled":                  "false",
		"ldap_server":                   "",
		"ldap_port":                     "389",
		"ldap_use_tls":                  "false",
		"ldap_verify_cert":              "true",
		"ldap_ca_cert":                  "",
		"ldap_base_dn":                  "",
		"ldap_bind_dn":                  "",
		"ldap_bind_password":            "",
		"ldap_user_filter":              "(uid=%s)",
		"ldap_username_attr":            "uid",
		"ldap_email_attr":               "mail",
		"ldap_default_role":             "viewer",
		"ldap_auto_provision":           "true",
		"encryption_key":                "",
		"cors_origins":                  strings.TrimSpace(os.Getenv("CORS_ORIGINS")),
		"https_enabled":                 "false",
		"https_redirect":                "false",
		"notifications_enabled":         "true",
		"smtp_enabled":                  "false",
		"smtp_host":                     "",
		"smtp_port":                     "587",
		"smtp_username":                 "",
		"smtp_password":                 "",
		"smtp_from":                     "",
		"smtp_use_tls":                  "true",
		"device_silence_check_minutes":  "5",
		"relay_ingestion_enabled":       "false",
		"relay_central_host":            "",
		"security_max_failed_attempts":     "",
		"security_lockout_duration_min":    "",
		"security_password_min_length":     "8",
		"security_password_max_length":     "128",
		"security_password_require_upper":  "true",
		"security_password_require_lower":  "true",
		"security_password_require_digit":  "true",
		"security_password_require_special":"true",
		"security_password_history_count":  "12",
		"security_password_expiry_days":    "90",
	}

	insertSQL := `INSERT INTO app_settings (key, value, description) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO NOTHING`

	for k, v := range settings {
		var desc string
		switch k {
		case "retention_days":
			desc = "Days to keep logs before auto-deletion"
		case "session_timeout_min":
			desc = "Session timeout in minutes"
		case "session_remembered_max_days":
			desc = "Max lifetime in days for remembered sessions"
		case "is_initialized":
			desc = "Application initialization flag"
		case "default_language":
			desc = "Default UI language for new sessions"
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
		case "https_enabled":
			desc = "Enable HTTPS on the reverse proxy"
		case "https_redirect":
			desc = "Redirect HTTP traffic to HTTPS"
		case "notifications_enabled":
			desc = "Enable the alert notification system"
		case "smtp_enabled":
			desc = "Enable SMTP email delivery"
		case "smtp_host":
			desc = "SMTP server hostname for email notifications"
		case "smtp_port":
			desc = "SMTP server port"
		case "smtp_username":
			desc = "SMTP authentication username"
		case "smtp_password":
			desc = "SMTP authentication password"
		case "smtp_from":
			desc = "From address for notification emails"
		case "smtp_use_tls":
			desc = "Use STARTTLS for the SMTP connection"
		case "device_silence_check_minutes":
			desc = "How often to check for silent devices (minutes)"
		case "relay_ingestion_enabled":
			desc = "Accept syslog forwarded by remote relays over mTLS (Admin > Syslog Relay)"
		case "relay_central_host":
			desc = "This server's hostname/IP as reachable from a relay's VLAN, pre-filled into generated relay.conf bundles"
		case "security_max_failed_attempts":
			desc = "Max failed login attempts before account lockout (empty = use MAX_FAILED_ATTEMPTS env or default 5)"
		case "security_lockout_duration_min":
			desc = "Account lockout duration in minutes (empty = use LOCKOUT_DURATION_MIN env or default 15)"
		case "security_password_min_length":
			desc = "Minimum password length"
		case "security_password_max_length":
			desc = "Maximum password length"
		case "security_password_require_upper":
			desc = "Require at least one uppercase letter"
		case "security_password_require_lower":
			desc = "Require at least one lowercase letter"
		case "security_password_require_digit":
			desc = "Require at least one digit"
		case "security_password_require_special":
			desc = "Require at least one special character"
		case "security_password_history_count":
			desc = "Number of recent passwords to remember (0 = disabled)"
		case "security_password_expiry_days":
			desc = "Password expiry period in days (0 = disabled)"
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
	if (key == "ldap_bind_password" || key == "smtp_password") && val != "" {
		// Encryption key comes only from the environment (ENCRYPTION_KEY /
		// ENCRYPTION_KEY_FILE), never the database - see util.SecretFromEnv and
		// the "secrets at rest" note in README.
		if decrypted, err := util.Decrypt(val); err == nil {
			return decrypted
		}
	}
	return val
}

// UpdateSetting updates a setting value, encrypting sensitive fields
func UpdateSetting(db *sql.DB, key, value string) error {
	if (key == "ldap_bind_password" || key == "smtp_password") && value != "" {
		if encrypted, err := util.Encrypt(value); err == nil {
			value = encrypted
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

type envSetting struct {
	envVar  string
	key     string
	def     string
	isBool  bool
}

var envSettings = []envSetting{
	{"HTTPS_ENABLED", "https_enabled", "false", true},
	{"HTTPS_REDIRECT", "https_redirect", "false", true},
	{"MAX_FAILED_ATTEMPTS", "security_max_failed_attempts", "5", false},
	{"LOCKOUT_DURATION_MIN", "security_lockout_duration_min", "15", false},
	{"SESSION_TIMEOUT_MIN", "session_timeout_min", "15", false},
	{"PASSWORD_MIN_LENGTH", "security_password_min_length", "8", false},
	{"PASSWORD_MAX_LENGTH", "security_password_max_length", "128", false},
	{"PASSWORD_REQUIRE_UPPER", "security_password_require_upper", "true", true},
	{"PASSWORD_REQUIRE_LOWER", "security_password_require_lower", "true", true},
	{"PASSWORD_REQUIRE_DIGIT", "security_password_require_digit", "true", true},
	{"PASSWORD_REQUIRE_SPECIAL", "security_password_require_special", "true", true},
	{"PASSWORD_HISTORY_COUNT", "security_password_history_count", "12", false},
}

// ApplyEnvSettingOverrides populates app_settings rows that are still empty
// with a value from the matching environment variable or the built-in default.
// Each key is only filled once, tracked via a "<key>_env_applied" marker, so
// an admin's subsequent UI edits are never silently overwritten on restart.
func ApplyEnvSettingOverrides(db *sql.DB) {
	for _, es := range envSettings {
		raw := strings.TrimSpace(os.Getenv(es.envVar))
		appliedMarker := es.key + "_env_applied"
		if GetSetting(db, appliedMarker, "false") == "true" {
			continue
		}
		current := GetSetting(db, es.key, "")
		if current != "" {
			continue
		}
		value := es.def
		if raw != "" {
			if es.isBool {
				if _, err := strconv.ParseBool(raw); err != nil {
					slog.Warn("invalid boolean value for env setting override, ignoring", "env", es.envVar, "value", raw)
					continue
				}
			}
			value = raw
		}
		if err := UpdateSetting(db, es.key, value); err != nil {
			slog.Warn("failed to apply env setting override", "env", es.envVar, "key", es.key, "error", err)
			continue
		}
		if err := UpdateSetting(db, appliedMarker, "true"); err != nil {
			slog.Warn("failed to record env setting override as applied", "env", es.envVar, "key", es.key, "error", err)
		}
		src := "default"
		if raw != "" {
			src = es.envVar
		}
		slog.Info("applied setting from environment variable (first run only)", "env", es.envVar, "key", es.key, "value", value, "source", src)
	}

	if GetSetting(db, "https_enabled", "false") == "true" {
		sslDir := os.Getenv("SSL_DIR")
		if sslDir == "" {
			sslDir = "/data/ssl"
		}
		certPath := filepath.Join(sslDir, "server.crt")
		keyPath := filepath.Join(sslDir, "server.key")
		if _, err := os.Stat(certPath); os.IsNotExist(err) {
			slog.Warn("https_enabled is true but SSL certificate is missing", "path", certPath)
		}
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			slog.Warn("https_enabled is true but SSL private key is missing", "path", keyPath)
		}
	}
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

var partitionNameRe = regexp.MustCompile(`^syslog_logs_\d{4}_\d{2}$`)

// CleanupOldLogs deletes logs older than retentionDays. When syslog_logs is
// partitioned by month, whole partitions that fall entirely before the
// retention cutoff are dropped outright - this is near-instant and avoids
// scanning/vacuuming millions of rows one by one. Only the partition
// straddling the cutoff (plus the default partition, if any) falls back to
// batched row deletes.
func CleanupOldLogs(db *sql.DB, retentionDays int) (int64, error) {
	var isPartitioned bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_class WHERE relname = 'syslog_logs' AND relkind = 'p')").Scan(&isPartitioned); err != nil {
		return 0, fmt.Errorf("check partitioning: %w", err)
	}

	if !isPartitioned {
		return deleteOldLogsBatched(db, retentionDays)
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)

	rows, err := db.Query(`
		SELECT c.relname
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = 'syslog_logs' AND c.relname ~ '^syslog_logs_\d{4}_\d{2}$'
	`)
	if err != nil {
		return 0, fmt.Errorf("list partitions: %w", err)
	}
	var partitions []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return 0, err
		}
		partitions = append(partitions, name)
	}
	rows.Close()

	var totalDeleted int64
	for _, name := range partitions {
		if !partitionNameRe.MatchString(name) {
			continue
		}
		var y, m int
		if _, err := fmt.Sscanf(name, "syslog_logs_%d_%d", &y, &m); err != nil {
			continue
		}
		partEnd := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
		if partEnd.After(cutoff) {
			continue
		}

		var estRows float64
		_ = db.QueryRow("SELECT reltuples FROM pg_class WHERE relname = $1", name).Scan(&estRows)

		if _, err := db.Exec("DROP TABLE IF EXISTS " + name); err != nil {
			slog.Error("failed to drop expired partition", "partition", name, "err", err)
			continue
		}
		if estRows > 0 {
			totalDeleted += int64(estRows)
		}
		slog.Info("dropped expired partition", "partition", name, "approx_rows", estRows)
	}

	deleted, err := deleteOldLogsBatched(db, retentionDays)
	totalDeleted += deleted
	return totalDeleted, err
}

func deleteOldLogsBatched(db *sql.DB, retentionDays int) (int64, error) {
	const batchSize = 10000
	var totalDeleted int64

	for {
		result, err := db.Exec(
			"DELETE FROM syslog_logs WHERE ctid IN (SELECT ctid FROM syslog_logs WHERE timestamp < NOW() - ($1 || ' days')::interval LIMIT $2)",
			retentionDays, batchSize,
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
	ID                  int64      `json:"id"`
	Username            string     `json:"username"`
	Email               string     `json:"email"`
	PasswordHash        string     `json:"-"`
	Role                string     `json:"role"`
	AuthType            string     `json:"auth_type"`
	IsAdmin             bool       `json:"is_admin"`
	IsActive            bool       `json:"is_active"`
	CreatedAt           time.Time  `json:"created_at"`
	LastLoginAt         *time.Time `json:"last_login_at"`
	FailedLoginAttempts int        `json:"failed_login_attempts"`
	LockedUntil         *time.Time `json:"locked_until"`
	PasswordChangedAt   *time.Time `json:"password_changed_at"`
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

// UserSummary is the minimal, non-sensitive projection of a user - just
// enough to label a "pick a user" control. See handler.ListUserDirectory.
type UserSummary struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

func GetUserDirectory(db *sql.DB) ([]UserSummary, error) {
	rows, err := db.Query("SELECT id, username FROM users WHERE is_active ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []UserSummary
	for rows.Next() {
		var u UserSummary
		if err := rows.Scan(&u.ID, &u.Username); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func GetAllUsers(db *sql.DB) ([]User, error) {
	rows, err := db.Query("SELECT id, username, email, role, auth_type, is_admin, is_active, created_at, last_login_at, COALESCE(failed_login_attempts, 0), locked_until, password_changed_at FROM users ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.AuthType, &u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.LastLoginAt, &u.FailedLoginAttempts, &u.LockedUntil, &u.PasswordChangedAt); err != nil {
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
		"INSERT INTO users (username, password_hash, email, is_admin, role, is_active, auth_type, password_changed_at) VALUES ($1, $2, $3, $4, $5, $6, 'local', NOW()) RETURNING id, created_at",
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
	_, err := db.Exec("UPDATE users SET password_hash = $1, password_changed_at = NOW() WHERE id = $2", passwordHash, id)
	return err
}

// AddPasswordHistory stores a password hash in the history table for reuse checking.
func AddPasswordHistory(db *sql.DB, userID int64, passwordHash string) error {
	_, err := db.Exec("INSERT INTO password_history (user_id, password_hash) VALUES ($1, $2)", userID, passwordHash)
	return err
}

// CheckPasswordHistory returns true if the given hash matches any of the last
// N passwords for the user, where N is the history_count setting (default 12).
func CheckPasswordHistory(db *sql.DB, userID int64, passwordHash string) (bool, error) {
	historyCount := 12
	if v, err := strconv.Atoi(GetSetting(db, "security_password_history_count", "12")); err == nil && v > 0 {
		historyCount = v
	}
	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM (SELECT password_hash FROM password_history WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2) sub WHERE password_hash = $3`,
		userID, historyCount, passwordHash,
	).Scan(&count)
	return count > 0, err
}

// TrimPasswordHistory keeps only 2x the history_count entries per user to
// prevent unbounded growth of the history table.
func TrimPasswordHistory(db *sql.DB, userID int64) error {
	historyCount := 12
	if v, err := strconv.Atoi(GetSetting(db, "security_password_history_count", "12")); err == nil && v > 0 {
		historyCount = v
	}
	keep := historyCount * 2
	_, err := db.Exec(
		`DELETE FROM password_history WHERE ctid NOT IN (SELECT ctid FROM password_history WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2)`,
		userID, keep,
	)
	return err
}

// GetPasswordExpiryDays reads the configured password expiry period in days.
// A value of 0 means password expiry is disabled.
func GetPasswordExpiryDays(db *sql.DB) int {
	v := GetSetting(db, "security_password_expiry_days", "90")
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	return 0
}

// GetPasswordExpiryDate returns the date at which the user's password expires,
// or nil if expiry is disabled or the user has no password_changed_at.
func GetPasswordExpiryDate(db *sql.DB, userID int64) (*time.Time, error) {
	expiryDays := GetPasswordExpiryDays(db)
	if expiryDays == 0 {
		return nil, nil
	}
	var changedAt *time.Time
	err := db.QueryRow("SELECT password_changed_at FROM users WHERE id = $1", userID).Scan(&changedAt)
	if err != nil {
		return nil, err
	}
	if changedAt == nil {
		return nil, nil
	}
	exp := changedAt.AddDate(0, 0, expiryDays)
	return &exp, nil
}

// IsPasswordExpired returns true if the user's password has expired.
// It returns false if expiry is disabled or the user has no password_changed_at.
func IsPasswordExpired(db *sql.DB, userID int64) (bool, error) {
	expiryDays := GetPasswordExpiryDays(db)
	if expiryDays == 0 {
		return false, nil
	}
	var changedAt *time.Time
	err := db.QueryRow("SELECT password_changed_at FROM users WHERE id = $1", userID).Scan(&changedAt)
	if err != nil {
		return false, err
	}
	if changedAt == nil {
		return false, nil
	}
	return time.Now().After(changedAt.AddDate(0, 0, expiryDays)), nil
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

// ---- Lockout helpers ----

func CheckUserLockout(db *sql.DB, userID int64) (bool, error) {
	var locked bool
	err := db.QueryRow("SELECT COALESCE(locked_until, NOW()) > NOW() FROM users WHERE id = $1", userID).Scan(&locked)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return locked, err
}

// IncrementFailedLogins records one more failed login attempt and returns the
// resulting attempt count and whether this call just locked the account, so
// callers can log an accurate count without a second round-trip.
func IncrementFailedLogins(db *sql.DB, userID int64) (newFailed int, locked bool, err error) {
	maxFail := maxFailedAttempts(db)
	lockoutDur := failedLockoutDuration(db)
	// Only increment failures for users who are NOT currently locked.
	// For already-locked users, leave both fields untouched so the admin
	// unlock is not silently overridden by a stray failed login.
	// When the lockout expires (locked_until < NOW), treat the user as
	// unlocked: a single new failure can re-lock them if it reaches the threshold.
	var curFailed int
	var curLockedUntil sql.NullTime
	var hasLockout bool
	err = db.QueryRow("SELECT failed_login_attempts, locked_until, locked_until IS NOT NULL FROM users WHERE id = $1", userID).Scan(&curFailed, &curLockedUntil, &hasLockout)
	if err != nil {
		return 0, false, err
	}
	// Skip if actively locked
	if hasLockout && curLockedUntil.Valid && curLockedUntil.Time.After(time.Now()) {
		return curFailed, true, nil
	}
	newFailed = curFailed + 1
	locked = newFailed >= maxFail
	if locked {
		_, err = db.Exec("UPDATE users SET failed_login_attempts = $2, locked_until = NOW() + $3::interval WHERE id = $1", userID, newFailed, fmt.Sprintf("%d minutes", int(lockoutDur.Minutes())))
	} else {
		_, err = db.Exec("UPDATE users SET failed_login_attempts = $2, locked_until = NULL WHERE id = $1", userID, newFailed)
	}
	return newFailed, locked, err
}

func ResetFailedLogins(db *sql.DB, userID int64) error {
	_, err := db.Exec("UPDATE users SET failed_login_attempts = 0, locked_until = NULL WHERE id = $1", userID)
	return err
}

func UnlockUser(db *sql.DB, userID int64) error {
	return ResetFailedLogins(db, userID)
}

func maxFailedAttempts(db *sql.DB) int {
	s := GetSetting(db, "security_max_failed_attempts", "")
	if s == "" {
		s = os.Getenv("MAX_FAILED_ATTEMPTS")
	}
	if s == "" {
		return 5
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 5
	}
	return n
}

func failedLockoutDuration(db *sql.DB) time.Duration {
	s := GetSetting(db, "security_lockout_duration_min", "")
	if s == "" {
		s = os.Getenv("LOCKOUT_DURATION_MIN")
	}
	if s == "" {
		return 15 * time.Minute
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 15 * time.Minute
	}
	return time.Duration(n) * time.Minute
}

// ---- JWT Blacklist helpers ----

func BlacklistJTI(db *sql.DB, jti string) error {
	_, err := db.Exec("INSERT INTO jwt_blacklist (jti) VALUES ($1) ON CONFLICT DO NOTHING", jti)
	return err
}

func IsJTIBlacklisted(db *sql.DB, jti string) (bool, error) {
	var exists bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM jwt_blacklist WHERE jti = $1)", jti).Scan(&exists)
	return exists, err
}

func CleanupExpiredBlacklist(db *sql.DB) error {
	ttl := os.Getenv("JWT_BLACKLIST_TTL")
	if ttl == "" {
		ttl = "7 days"
	}
	_, err := db.Exec("DELETE FROM jwt_blacklist WHERE blacklisted_at < NOW() - $1::interval", ttl)
	return err
}

func GetUserByUsername(db *sql.DB, username string) (*User, error) {
	var u User
	err := db.QueryRow(
		"SELECT id, username, email, password_hash, role, is_admin, is_active, created_at, last_login_at, password_changed_at FROM users WHERE username = $1",
		username,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.LastLoginAt, &u.PasswordChangedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func GetUserByID(db *sql.DB, id int64) (*User, error) {
	var u User
	err := db.QueryRow(
		"SELECT id, username, email, password_hash, role, is_admin, is_active, created_at, last_login_at, password_changed_at FROM users WHERE id = $1",
		id,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.LastLoginAt, &u.PasswordChangedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func IsUserAdmin(db *sql.DB, userID int64) bool {
	var isAdmin bool
	err := db.QueryRow("SELECT is_admin FROM users WHERE id = $1", userID).Scan(&isAdmin)
	return err == nil && isAdmin
}

func RefreshMaterializedViews(db *sql.DB) {
	slog.Info("refreshing materialized views")
	_, err1 := db.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY mv_dashboard_summary")
	_, err2 := db.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY mv_dashboard_severity")
	_, err3 := db.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY mv_device_stats")
	_, err4 := db.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY mv_dashboard_top_errors")
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		slog.Error("materialized view refresh failed", "err1", err1, "err2", err2, "err3", err3, "err4", err4)
	}
	slog.Info("materialized views refreshed")
}

type AuditLogFilter struct {
	Username string
	Action   string
	From     string
	To       string
	Limit    int
	Offset   int
}

func GetAuditLogs(db *sql.DB, filter AuditLogFilter) ([]AuditLog, int64, error) {
	whereConditions := []string{}
	args := []interface{}{}
	argIdx := 1

	if filter.Username != "" {
		whereConditions = append(whereConditions, fmt.Sprintf("username ILIKE $%d", argIdx))
		args = append(args, "%"+filter.Username+"%")
		argIdx++
	}
	if filter.Action != "" {
		whereConditions = append(whereConditions, fmt.Sprintf("action = $%d", argIdx))
		args = append(args, filter.Action)
		argIdx++
	}
	if filter.From != "" {
		whereConditions = append(whereConditions, fmt.Sprintf("created_at >= $%d", argIdx))
		args = append(args, filter.From)
		argIdx++
	}
	if filter.To != "" {
		whereConditions = append(whereConditions, fmt.Sprintf("created_at <= $%d", argIdx))
		args = append(args, filter.To)
		argIdx++
	}

	whereClause := ""
	if len(whereConditions) > 0 {
		whereClause = "WHERE " + strings.Join(whereConditions, " AND ")
	}

	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM audit_log %s", whereClause)
	var total int64
	if err := db.QueryRow(countSQL, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	queryArgs := make([]interface{}, len(args)+2)
	copy(queryArgs, args)
	queryArgs[len(args)] = filter.Limit
	queryArgs[len(args)+1] = filter.Offset
	querySQL := fmt.Sprintf(
		"SELECT id, user_id, username, action, ip, details, created_at FROM audit_log %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d",
		whereClause, argIdx, argIdx+1,
	)

	rows, err := db.Query(querySQL, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []AuditLog
	for rows.Next() {
		var a AuditLog
		if err := rows.Scan(&a.ID, &a.UserID, &a.Username, &a.Action, &a.IP, &a.Details, &a.CreatedAt); err != nil {
			continue
		}
		logs = append(logs, a)
	}

	return logs, total, rows.Err()
}
