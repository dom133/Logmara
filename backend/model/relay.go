package model

import "time"

// Relay certificate lifecycle states. There is no real X.509 CRL/OCSP in
// v1 - "revoked" cuts a cert off at the transport layer instead, by
// dropping its CommonName from the mTLS listener's PermittedPeer list
// (see handler.writeRelayACL) and, if its whitelist entry hasn't since
// been relinked to a replacement certificate, also from the IP allow-list.
// See handler.RevokeRelayCertificate.
const (
	RelayCertStatusIssued  = "issued"
	RelayCertStatusRevoked = "revoked"
)

// RelayCertRenewalWindowDays is how close to its own expiry an "issued"
// certificate must be before RegenerateRelayCertificate will renew it (see
// handler/relay.go) - matches the frontend's renewal-eligibility check in
// SyslogRelay.tsx, which shows the "Renew" action under the same threshold.
const RelayCertRenewalWindowDays = 30

// RelayWhitelistEntry is one IP address allowed to send syslog to the
// central mTLS relay listener (port 6514). RelayCertID is set when the
// entry was created as part of issuing a relay certificate (the normal
// path via CreateRelayCertificate); it's nil for entries added directly
// through the whitelist tab without a matching certificate.
type RelayWhitelistEntry struct {
	ID          int64     `json:"id"`
	IPAddress   string    `json:"ip_address"`
	Label       string    `json:"label"`
	RelayCertID *int64    `json:"relay_cert_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   *int64    `json:"created_by,omitempty"`
}

type RelayWhitelistRequest struct {
	IPAddress string `json:"ip_address" binding:"required,ip"`
	Label     string `json:"label" binding:"required,max=255"`
}

// RelayCertificate is metadata only - the private key is never stored
// server-side (see relaypki.IssueClientCert) and the response that issues
// it is the only time the caller can retrieve the key material.
type RelayCertificate struct {
	ID          int64      `json:"id"`
	Label       string     `json:"label"`
	SerialHex   string     `json:"serial_hex"`
	Fingerprint string     `json:"fingerprint_sha256"`
	Status      string     `json:"status"`
	IssuedAt    time.Time  `json:"issued_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	IssuedBy    *int64     `json:"issued_by,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// RelayCertificateRequest issues a certificate and whitelists IPAddress for
// it in the same step - a cert alone grants nothing without a matching
// whitelist entry (see backend/handler/relay.go writeRelayACL).
type RelayCertificateRequest struct {
	Label     string `json:"label" binding:"required,max=255"`
	IPAddress string `json:"ip_address" binding:"required,ip"`
}

// RelayACLEntry pairs an active whitelist entry's IP with the exact peer
// name (CommonName) its currently-linked certificate was issued with - see
// db.GetActiveRelayACLEntries and handler.writeRelayACL, which uses both
// halves to build the IP allow-list and the mTLS listener's
// PermittedPeer list.
type RelayACLEntry struct {
	IPAddress string
	PeerName  string
}
