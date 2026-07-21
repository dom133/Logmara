package db

import (
	"database/sql"
	"fmt"

	"syslytics/model"
)

func GetRelayWhitelist(db *sql.DB) ([]model.RelayWhitelistEntry, error) {
	rows, err := db.Query(`SELECT id, ip_address, label, relay_cert_id, created_at, created_by
		FROM relay_whitelist ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list relay whitelist: %w", err)
	}
	defer rows.Close()

	var entries []model.RelayWhitelistEntry
	for rows.Next() {
		var e model.RelayWhitelistEntry
		var certID, createdBy sql.NullInt64
		if err := rows.Scan(&e.ID, &e.IPAddress, &e.Label, &certID, &e.CreatedAt, &createdBy); err != nil {
			return nil, fmt.Errorf("scan relay whitelist entry: %w", err)
		}
		if certID.Valid {
			e.RelayCertID = &certID.Int64
		}
		if createdBy.Valid {
			e.CreatedBy = &createdBy.Int64
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// AddRelayWhitelistEntry inserts a new allowed relay IP. relayCertID is nil
// for entries added directly (not as part of issuing a certificate).
func AddRelayWhitelistEntry(db *sql.DB, ip, label string, relayCertID *int64, createdBy int64) (*model.RelayWhitelistEntry, error) {
	e := model.RelayWhitelistEntry{IPAddress: ip, Label: label, RelayCertID: relayCertID, CreatedBy: &createdBy}
	err := db.QueryRow(
		`INSERT INTO relay_whitelist (ip_address, label, relay_cert_id, created_by) VALUES ($1, $2, $3, $4)
		 RETURNING id, created_at`,
		ip, label, relayCertID, createdBy,
	).Scan(&e.ID, &e.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert relay whitelist entry: %w", err)
	}
	return &e, nil
}

func DeleteRelayWhitelistEntry(db *sql.DB, id int64) error {
	_, err := db.Exec("DELETE FROM relay_whitelist WHERE id = $1", id)
	return err
}

// DeleteRelayWhitelistByCertID drops whatever whitelist entry was created
// alongside certID, used when revoking a certificate - since v1 has no
// X.509 CRL, this ACL removal is what actually cuts the relay off.
func DeleteRelayWhitelistByCertID(db *sql.DB, certID int64) error {
	_, err := db.Exec("DELETE FROM relay_whitelist WHERE relay_cert_id = $1", certID)
	return err
}

func GetRelayCertificates(db *sql.DB) ([]model.RelayCertificate, error) {
	rows, err := db.Query(`SELECT id, label, serial_hex, fingerprint_sha256, status, issued_at, issued_by, revoked_at
		FROM relay_certificates ORDER BY issued_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list relay certificates: %w", err)
	}
	defer rows.Close()

	var certs []model.RelayCertificate
	for rows.Next() {
		var c model.RelayCertificate
		var issuedBy sql.NullInt64
		var revokedAt sql.NullTime
		if err := rows.Scan(&c.ID, &c.Label, &c.SerialHex, &c.Fingerprint, &c.Status, &c.IssuedAt, &issuedBy, &revokedAt); err != nil {
			return nil, fmt.Errorf("scan relay certificate: %w", err)
		}
		if issuedBy.Valid {
			c.IssuedBy = &issuedBy.Int64
		}
		if revokedAt.Valid {
			c.RevokedAt = &revokedAt.Time
		}
		certs = append(certs, c)
	}
	return certs, rows.Err()
}

func InsertRelayCertificate(db *sql.DB, label, serialHex, fingerprint string, issuedBy int64) (int64, error) {
	var id int64
	err := db.QueryRow(
		`INSERT INTO relay_certificates (label, serial_hex, fingerprint_sha256, status, issued_by)
		 VALUES ($1, $2, $3, 'issued', $4) RETURNING id`,
		label, serialHex, fingerprint, issuedBy,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert relay certificate: %w", err)
	}
	return id, nil
}

func RevokeRelayCertificate(db *sql.DB, id int64) error {
	_, err := db.Exec(`UPDATE relay_certificates SET status = 'revoked', revoked_at = NOW() WHERE id = $1`, id)
	return err
}
