package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
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
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migration failed (%s): %w", stmt[:50], err)
		}
	}

	if err := seedParsers(db); err != nil {
		log.Printf("Warning: seeding parsers failed: %v", err)
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

	type fieldSeed struct {
		Name  string
		Label string
		Type  string
	}

	type parserSeed struct {
		Name        string
		Description string
		DeviceType  string
		MatchType   string
		MatchValue  string
		Regex       string
		Fields      []fieldSeed
	}

	parsers := []parserSeed{
		{
			Name:        "MikroTik Interface Status",
			Description: "Matches MikroTik interface up/down events",
			DeviceType:  "mikrotik",
			MatchType:   "hostname",
			MatchValue:  "mikrotik*",
			Regex:       `interface\s+(\S+)\s+link\s+(up|down)(?:\s+on\s+the\s+(\S+))?`,
			Fields: []fieldSeed{
				{Name: "interface", Label: "Interface", Type: "string"},
				{Name: "status", Label: "Link Status", Type: "string"},
				{Name: "cause", Label: "Cause", Type: "string"},
			},
		},
		{
			Name:        "MikroTik DHCP Lease",
			Description: "Matches MikroTik DHCP lease events",
			DeviceType:  "mikrotik",
			MatchType:   "hostname",
			MatchValue:  "mikrotik*",
			Regex:       `DHCPLease:(\S+)\s+address=(\d+\.\d+\.\d+\.\d+)\s+mac-address=([0-9A-Fa-f:-]+)`,
			Fields: []fieldSeed{
				{Name: "lease_action", Label: "Lease Action", Type: "string"},
				{Name: "ip_address", Label: "IP Address", Type: "string"},
				{Name: "mac_address", Label: "MAC Address", Type: "string"},
			},
		},
		{
			Name:        "MikroTik User Login",
			Description: "Matches MikroTik user login/logout events",
			DeviceType:  "mikrotik",
			MatchType:   "hostname",
			MatchValue:  "mikrotik*",
			Regex:       `User\s+(\S+)\s+logged\s+(in|out)\s+from\s+(\S+)`,
			Fields: []fieldSeed{
				{Name: "username", Label: "Username", Type: "string"},
				{Name: "login_action", Label: "Action", Type: "string"},
				{Name: "source_ip", Label: "Source IP", Type: "string"},
			},
		},
		{
			Name:        "Ubiquiti AP Event",
			Description: "Matches Ubiquiti UniFi AP connect/disconnect/reboot events",
			DeviceType:  "ubiquiti",
			MatchType:   "hostname",
			MatchValue:  "ubnt*",
			Regex:       `AP\s+([0-9A-Fa-f:]+)\s+(\S+)\s+on\s+(\S+)`,
			Fields: []fieldSeed{
				{Name: "mac_address", Label: "MAC Address", Type: "string"},
				{Name: "event_type", Label: "Event Type", Type: "string"},
				{Name: "site", Label: "Site", Type: "string"},
			},
		},
		{
			Name:        "Ubiquiti Client Connect",
			Description: "Matches Ubiquiti client association events",
			DeviceType:  "ubiquiti",
			MatchType:   "hostname",
			MatchValue:  "ubnt*",
			Regex:       `client\s+([0-9A-Fa-f:]+)\s+(\S+)\s+on\s+(\S+)\s+channel\s+(\d+)$`,
			Fields: []fieldSeed{
				{Name: "client_mac", Label: "Client MAC", Type: "string"},
				{Name: "action", Label: "Action", Type: "string"},
				{Name: "ssid", Label: "SSID", Type: "string"},
				{Name: "channel", Label: "Channel", Type: "string"},
			},
		},
		{
			Name:        "Cisco IOS Interface",
			Description: "Matches Cisco IOS interface up/down %LINK messages",
			DeviceType:  "cisco",
			MatchType:   "hostname",
			MatchValue:  "cisco*",
			Regex:       `%LINK-(\d+)-(UPDN):\s+Interface\s+(\S+),\s+(change|condition)\s+(is\s+\S+|state\s+\S+)`,
			Fields: []fieldSeed{
				{Name: "msec", Label: "MSEC Code", Type: "string"},
				{Name: "interface", Label: "Interface", Type: "string"},
				{Name: "status", Label: "Status", Type: "string"},
			},
		},
		{
			Name:        "Cisco IOS BGP",
			Description: "Matches Cisco IOS BGP state change messages",
			DeviceType:  "cisco",
			MatchType:   "hostname",
			MatchValue:  "cisco*",
			Regex:       `%BGP-5-ADJCHANGE:\s+Neighbor\s+(\d+\.\d+\.\d+\.\d+)\s+session\s+(Down|Up)`,
			Fields: []fieldSeed{
				{Name: "neighbor_ip", Label: "Neighbor IP", Type: "string"},
				{Name: "session_state", Label: "Session State", Type: "string"},
			},
		},
		{
			Name:        "Cisco IOS Authentication",
			Description: "Matches Cisco authentication success/failure",
			DeviceType:  "cisco",
			MatchType:   "hostname",
			MatchValue:  "cisco*",
			Regex:       `%SEC_LOGIN-\d+-(\S+):\s+User=\S+,\s+Method=(\S+),\s+Reason=(\S+),\s+Info=(\S+)`,
			Fields: []fieldSeed{
				{Name: "auth_result", Label: "Result", Type: "string"},
				{Name: "method", Label: "Method", Type: "string"},
				{Name: "reason", Label: "Reason", Type: "string"},
				{Name: "info", Label: "Info", Type: "string"},
			},
		},
		{
			Name:        "Palo Alto Threat",
			Description: "Matches Palo Alto threat log entries",
			DeviceType:  "palo_alto",
			MatchType:   "hostname",
			MatchValue:  "pan*",
			Regex:       `threat.*?src=(\d+\.\d+\.\d+\.\d+).*?dst=(\d+\.\d+\.\d+\.\d+).*?action=(\S+).*?category=(\S+)`,
			Fields: []fieldSeed{
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Destination IP", Type: "string"},
				{Name: "action", Label: "Action", Type: "string"},
				{Name: "category", Label: "Category", Type: "string"},
			},
		},
		{
			Name:        "Palo Alto Traffic",
			Description: "Matches Palo Alto traffic log entries",
			DeviceType:  "palo_alto",
			MatchType:   "hostname",
			MatchValue:  "pan*",
			Regex:       `traffic.*?src=(\d+\.\d+\.\d+\.\d+).*?dst=(\d+\.\d+\.\d+\.\d+).*?proto=(\S+).*?action=(\S+)`,
			Fields: []fieldSeed{
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Destination IP", Type: "string"},
				{Name: "protocol", Label: "Protocol", Type: "string"},
				{Name: "action", Label: "Action", Type: "string"},
			},
		},
		{
			Name:        "pfSense Filter Log",
			Description: "Matches pfSense firewall filter log entries",
			DeviceType:  "pfsense",
			MatchType:   "hostname",
			MatchValue:  "pfsense*",
			Regex:       `filter\+.*?(pass|block).*?(\S+)\s+(\d+\.\d+\.\d+\.\d+):\d+\s->\s(\d+\.\d+\.\d+\.\d+):\d+`,
			Fields: []fieldSeed{
				{Name: "action", Label: "Action", Type: "string"},
				{Name: "interface", Label: "Interface", Type: "string"},
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Destination IP", Type: "string"},
			},
		},
		{
			Name:        "Suricata Alert",
			Description: "Matches Suricata IDS/IPS alert entries",
			DeviceType:  "pfsense",
			MatchType:   "hostname",
			MatchValue:  "pfsense*",
			Regex:       `\[1:\d+:\d+\]\s+(\S+)\s+(\d+\.\d+\.\d+\.\d+):\d+\s->\s(\d+\.\d+\.\d+\.\d+):\d+`,
			Fields: []fieldSeed{
				{Name: "alert_msg", Label: "Alert Message", Type: "string"},
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Destination IP", Type: "string"},
			},
		},
		{
			Name:        "Generic IP Extraction",
			Description: "Generic catch-all for IP addresses in log messages",
			DeviceType:  "generic",
			MatchType:   "all",
			MatchValue:  "",
			Regex:       `(?:src|source|SRC|from)=(\d+\.\d+\.\d+\.\d+).*?(?:dst|dest|DEST|to)=(\d+\.\d+\.\d+\.\d+)`,
			Fields: []fieldSeed{
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Destination IP", Type: "string"},
			},
		},
		{
			Name:        "Generic MAC Extraction",
			Description: "Generic catch-all for MAC addresses in log messages",
			DeviceType:  "generic",
			MatchType:   "all",
			MatchValue:  "",
			Regex:       `(?:mac|MAC|ether|hwaddr|client)=([0-9A-Fa-f]{2}[:-][0-9A-Fa-f]{2}[:-][0-9A-Fa-f]{2}[:-][0-9A-Fa-f]{2}[:-][0-9A-Fa-f]{2}[:-][0-9A-Fa-f]{2})`,
			Fields: []fieldSeed{
				{Name: "mac_address", Label: "MAC Address", Type: "string"},
			},
		},
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	insertParser := `INSERT INTO parsers (name, description, device_type, match_type, match_value, regex, enabled, is_builtin)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`
	insertField := `INSERT INTO parsed_fields_registry (parser_id, field_name, field_label, field_type)
		VALUES ($1, $2, $3, $4)`

	for _, p := range parsers {
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
