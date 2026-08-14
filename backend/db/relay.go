package db

import (
	"database/sql"
	"fmt"
	"time"

	"logmara/model"
)

func scanRelayWhitelistRows(rows *sql.Rows) ([]model.RelayWhitelistEntry, error) {
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

func GetRelayWhitelist(db *sql.DB) ([]model.RelayWhitelistEntry, error) {
	rows, err := db.Query(`SELECT id, ip_address, label, relay_cert_id, created_at, created_by
		FROM relay_whitelist ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list relay whitelist: %w", err)
	}
	return scanRelayWhitelistRows(rows)
}

// GetActiveRelayACLEntries returns, for every whitelist entry whose linked
// certificate is currently "issued", its IP address paired with the exact
// peer name (CommonName) that certificate was issued with - what
// writeRelayACL uses to build both the IP allow-list and the
// PermittedPeer list that pins the mTLS handshake to that
// one certificate (see relaypki.IssueClientCert). An entry with no
// certificate yet, or whose certificate has been revoked or superseded, is
// excluded - a relay physically can't get in either way, whether or not
// it's still shown - "Blocked" - in the UI.
func GetActiveRelayACLEntries(db *sql.DB) ([]model.RelayACLEntry, error) {
	rows, err := db.Query(`SELECT w.ip_address, c.label, c.serial_hex
		FROM relay_whitelist w
		JOIN relay_certificates c ON c.id = w.relay_cert_id
		WHERE c.status = 'issued'
		ORDER BY w.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list active relay ACL entries: %w", err)
	}
	defer rows.Close()

	var entries []model.RelayACLEntry
	for rows.Next() {
		var ip, label, serialHex string
		if err := rows.Scan(&ip, &label, &serialHex); err != nil {
			return nil, fmt.Errorf("scan relay ACL entry: %w", err)
		}
		entries = append(entries, model.RelayACLEntry{IPAddress: ip, PeerName: label + "#" + serialHex})
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

// GetRelayWhitelistEntry returns sql.ErrNoRows verbatim (unwrapped) when id
// doesn't exist, matching GetUserByUsername/GetNotificationChannel's
// convention so callers can compare with == rather than errors.Is.
func GetRelayWhitelistEntry(db *sql.DB, id int64) (*model.RelayWhitelistEntry, error) {
	var e model.RelayWhitelistEntry
	var certID, createdBy sql.NullInt64
	err := db.QueryRow(
		`SELECT id, ip_address, label, relay_cert_id, created_at, created_by FROM relay_whitelist WHERE id = $1`,
		id,
	).Scan(&e.ID, &e.IPAddress, &e.Label, &certID, &e.CreatedAt, &createdBy)
	if err != nil {
		return nil, err
	}
	if certID.Valid {
		e.RelayCertID = &certID.Int64
	}
	if createdBy.Valid {
		e.CreatedBy = &createdBy.Int64
	}
	return &e, nil
}

// LinkRelayCertificateToWhitelist attaches a newly issued certificate to an
// existing whitelist entry, used when an admin generates a certificate for
// an IP that's already whitelisted instead of inserting a second (and
// rejected, since ip_address is unique) entry for it.
func LinkRelayCertificateToWhitelist(db *sql.DB, whitelistID, certID int64) error {
	_, err := db.Exec("UPDATE relay_whitelist SET relay_cert_id = $1 WHERE id = $2", certID, whitelistID)
	return err
}

// GetRelayWhitelistEntryByCertID finds whichever whitelist entry currently
// points at certID, if any - used to reissue a certificate for the same IP
// after the old one is revoked, without asking the admin to re-enter it.
// Returns sql.ErrNoRows if no entry references it (e.g. the whitelist entry
// itself was removed via DeleteRelayWhitelistEntry, which revokes its
// certificate but doesn't leave anything to search by).
func GetRelayWhitelistEntryByCertID(db *sql.DB, certID int64) (*model.RelayWhitelistEntry, error) {
	var e model.RelayWhitelistEntry
	var linkedCertID, createdBy sql.NullInt64
	err := db.QueryRow(
		`SELECT id, ip_address, label, relay_cert_id, created_at, created_by FROM relay_whitelist WHERE relay_cert_id = $1`,
		certID,
	).Scan(&e.ID, &e.IPAddress, &e.Label, &linkedCertID, &e.CreatedAt, &createdBy)
	if err != nil {
		return nil, err
	}
	if linkedCertID.Valid {
		e.RelayCertID = &linkedCertID.Int64
	}
	if createdBy.Valid {
		e.CreatedBy = &createdBy.Int64
	}
	return &e, nil
}

const relayCertColumns = `id, label, serial_hex, fingerprint_sha256, status, issued_at, expires_at, issued_by, revoked_at`

func scanRelayCertificate(scanner interface {
	Scan(dest ...any) error
}) (model.RelayCertificate, error) {
	var c model.RelayCertificate
	var expiresAt sql.NullTime
	var issuedBy sql.NullInt64
	var revokedAt sql.NullTime
	err := scanner.Scan(&c.ID, &c.Label, &c.SerialHex, &c.Fingerprint, &c.Status, &c.IssuedAt, &expiresAt, &issuedBy, &revokedAt)
	if err != nil {
		return c, err
	}
	if expiresAt.Valid {
		c.ExpiresAt = expiresAt.Time
	}
	if issuedBy.Valid {
		c.IssuedBy = &issuedBy.Int64
	}
	if revokedAt.Valid {
		c.RevokedAt = &revokedAt.Time
	}
	return c, nil
}

func GetRelayCertificates(db *sql.DB) ([]model.RelayCertificate, error) {
	rows, err := db.Query(`SELECT ` + relayCertColumns + ` FROM relay_certificates ORDER BY issued_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list relay certificates: %w", err)
	}
	defer rows.Close()

	var certs []model.RelayCertificate
	for rows.Next() {
		c, err := scanRelayCertificate(rows)
		if err != nil {
			return nil, fmt.Errorf("scan relay certificate: %w", err)
		}
		certs = append(certs, c)
	}
	return certs, rows.Err()
}

// GetRelayCertificate returns sql.ErrNoRows verbatim when id doesn't exist
// (see GetRelayWhitelistEntry's doc comment for why).
func GetRelayCertificate(db *sql.DB, id int64) (*model.RelayCertificate, error) {
	c, err := scanRelayCertificate(db.QueryRow(`SELECT `+relayCertColumns+` FROM relay_certificates WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetExpiringRelayCertificates returns "issued" certificates whose
// expires_at falls within the next window - used by
// alertengine.CheckRelayCertExpiring.
func GetExpiringRelayCertificates(db *sql.DB, window time.Duration) ([]model.RelayCertificate, error) {
	rows, err := db.Query(
		`SELECT `+relayCertColumns+` FROM relay_certificates
		 WHERE status = 'issued' AND expires_at IS NOT NULL AND expires_at <= NOW() + $1::interval
		 ORDER BY expires_at`,
		fmt.Sprintf("%d seconds", int64(window.Seconds())),
	)
	if err != nil {
		return nil, fmt.Errorf("list expiring relay certificates: %w", err)
	}
	defer rows.Close()

	var certs []model.RelayCertificate
	for rows.Next() {
		c, err := scanRelayCertificate(rows)
		if err != nil {
			return nil, fmt.Errorf("scan relay certificate: %w", err)
		}
		certs = append(certs, c)
	}
	return certs, rows.Err()
}

func InsertRelayCertificate(db *sql.DB, label, serialHex, fingerprint string, expiresAt time.Time, issuedBy int64) (int64, error) {
	var id int64
	err := db.QueryRow(
		`INSERT INTO relay_certificates (label, serial_hex, fingerprint_sha256, status, expires_at, issued_by)
		 VALUES ($1, $2, $3, 'issued', $4, $5) RETURNING id`,
		label, serialHex, fingerprint, expiresAt, issuedBy,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert relay certificate: %w", err)
	}
	return id, nil
}

// RevokeRelayCertificate marks a certificate revoked. It deliberately does
// not touch relay_whitelist - the linked entry (if any) stays whitelisted
// by IP, only the credential itself is retired; see
// handler.GenerateCertificateForWhitelistEntry and
// handler.RegenerateRelayCertificate for how a replacement gets issued.
func RevokeRelayCertificate(db *sql.DB, id int64) error {
	_, err := db.Exec(`UPDATE relay_certificates SET status = 'revoked', revoked_at = NOW() WHERE id = $1`, id)
	return err
}

// GetRelayCertificateStatus returns just a certificate's status, used to
// tell an active certificate (blocks reissuing for its whitelist entry)
// apart from a revoked one (doesn't).
func GetRelayCertificateStatus(db *sql.DB, id int64) (string, error) {
	var status string
	err := db.QueryRow("SELECT status FROM relay_certificates WHERE id = $1", id).Scan(&status)
	return status, err
}


