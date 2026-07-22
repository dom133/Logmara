package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"syslytics/audit"
	"syslytics/db"
	"syslytics/middleware"
	"syslytics/model"
	"syslytics/relaypki"

	"github.com/gin-gonic/gin"
)

// relayPKIDir is where the CA, the central listener's server cert, and the
// generated ACL live - a subdirectory of the same shared volume the api and
// rsyslog containers already both mount at /data (see docker-compose.yml).
func relayPKIDir() string {
	if v := os.Getenv("RELAY_PKI_DIR"); v != "" {
		return v
	}
	return "/data/relay"
}

func relayACLPath() string {
	return filepath.Join(relayPKIDir(), "allowed-relays.conf")
}

// relayHeartbeatPath returns where rsyslog/syslog.conf's RelayHeartbeatFile
// template (ruleset "relayAccept") touches a small file every time it
// forwards a batch from ip, or "" if ip isn't a valid address - defends
// against building a path from a malformed relay_whitelist.ip_address value
// (there's no format check on insert, see db.AddRelayWhitelistEntry).
func relayHeartbeatPath(ip string) string {
	if net.ParseIP(ip) == nil {
		return ""
	}
	return filepath.Join(relayPKIDir(), "heartbeat-"+ip)
}

// relayListenerRuleset names the ruleset that gates the mTLS relay
// listener - defined dynamically inside allowed-relays.conf (see
// writeRelayACL) rather than statically in rsyslog/syslog.conf, same as
// the input() below.
const relayListenerRuleset = "relayIngest"

// relaySentinelPeer is a StreamDriver.PermittedPeers placeholder that can
// never equal a real certificate's CommonName (see relaypki.IssueClientCert
// - every real one contains "#" followed by a hex serial). Used instead of
// an empty PermittedPeers array so the mTLS listener still binds - and
// stays unambiguously fail-closed - when relay ingestion is disabled or
// nothing currently qualifies, rather than relying on undocumented
// behavior for what an empty list means to the gtls driver.
const relaySentinelPeer = "no-relay-certificates-active"

// rsyslogStringLiteral quotes s for use inside a RainerScript string
// literal or array element (e.g. StreamDriver.PermittedPeers=[...]) -
// backslash and double-quote are the only two characters that syntax
// treats specially.
func rsyslogStringLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// writeRelayACL regenerates allowed-relays.conf, which is include()d at
// the top level of rsyslog/syslog.conf and supplies BOTH the ruleset that
// gates the mTLS relay listener by source IP AND the input() listener
// itself (port 6514) - the latter has to live here too, not statically in
// syslog.conf, because pinning the TLS handshake to the exact
// currently-issued certificate per relay (StreamDriver.AuthMode="x509/name"
// + PermittedPeers, matched against the CommonName relaypki.IssueClientCert
// gives each cert - label + "#" + serial) requires PermittedPeers to be
// regenerated every time a certificate is issued or revoked, and that
// parameter can only be set where the listener is declared.
//
// A valid client certificate alone is still not enough to get in: the
// peer's IP must also be on the current whitelist AND that whitelist
// entry's certificate must currently be "issued". Together these two
// layers are what make RevokeRelayCertificate (and, for the case this was
// added to fix, RegenerateRelayCertificate) an immediate, real cutoff for
// the OLD certificate specifically - not just any CA-signed one - rather
// than a database label alone. When the feature is disabled or nothing
// currently qualifies, every connection is dropped.
//
// Regenerating this file alone does not apply it: rsyslogd has no true
// config hot-reload (SIGHUP only reopens output files as of rsyslog
// 4.5.1+; $HUPisRestart is deprecated upstream and never set here) - see
// reloadRelayConfig, which asks entrypoint.sh's supervisor loop to
// actually restart the rsyslogd child process.
func writeRelayACL(database *sql.DB) error {
	enabled := db.GetSetting(database, "relay_ingestion_enabled", "false") == "true"

	var comment, rulesetBody string
	peerNames := []string{relaySentinelPeer}

	if !enabled {
		comment = "# Relay ingestion is disabled (Admin > Settings) - dropping all connections."
		rulesetBody = "    stop\n"
	} else {
		entries, err := db.GetActiveRelayACLEntries(database)
		if err != nil {
			return fmt.Errorf("load relay whitelist: %w", err)
		}
		if len(entries) == 0 {
			comment = "# Relay ingestion is enabled but no whitelisted IP currently has an active certificate - dropping all connections."
			rulesetBody = "    stop\n"
		} else {
			var b strings.Builder
			b.WriteString("    if ")
			for i, e := range entries {
				if i > 0 {
					b.WriteString(" or ")
				}
				fmt.Fprintf(&b, "$fromhost-ip == %q", e.IPAddress)
			}
			b.WriteString(" then {\n        call relayAccept\n    } else {\n        stop\n    }\n")
			rulesetBody = b.String()

			peerNames = make([]string, len(entries))
			for i, e := range entries {
				peerNames[i] = e.PeerName
			}
		}
	}

	peerLiterals := make([]string, len(peerNames))
	for i, p := range peerNames {
		peerLiterals[i] = rsyslogStringLiteral(p)
	}

	var content strings.Builder
	content.WriteString("# Auto-generated by the API (Admin > Syslog Relay) - do not edit manually.\n")
	if comment != "" {
		content.WriteString(comment + "\n")
	}
	fmt.Fprintf(&content, "ruleset(name=%q) {\n%s}\n\n", relayListenerRuleset, rulesetBody)
	fmt.Fprintf(&content, "input(type=\"imtcp\" port=\"6514\" ruleset=%q\n", relayListenerRuleset)
	content.WriteString("  StreamDriver.Name=\"gtls\"\n  StreamDriver.Mode=\"1\"\n  StreamDriver.AuthMode=\"x509/name\"\n")
	fmt.Fprintf(&content, "  StreamDriver.PermittedPeers=[%s]\n)\n", strings.Join(peerLiterals, ", "))

	dir := relayPKIDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create relay pki dir: %w", err)
	}
	tmpPath := relayACLPath() + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content.String()), 0644); err != nil {
		return fmt.Errorf("write relay acl: %w", err)
	}
	return os.Rename(tmpPath, relayACLPath())
}

// reloadRelayConfig asks the rsyslog container's reload sidecar (same HTTP
// pattern as the frontend's nginx reload sidecar, see postReload in
// admin.go - but NOT the same underlying mechanism: unlike nginx, rsyslogd
// has no lightweight config reload, so the sidecar's reload.sh actually
// kills the rsyslogd child and entrypoint.sh's supervisor loop restarts it
// against the regenerated allowed-relays.conf, briefly interrupting syslog
// ingestion on both 514 and 6514 while it comes back up) so the
// regenerated ACL takes effect. RSYSLOG_RELOAD_TARGETS_HOST mirrors
// NGINX_RELOAD_TARGETS_HOST: unset for the single-server/single-rsyslog
// case (docker-compose.yml), set to a DNS name resolving to every edge
// node's task IP (e.g. Swarm's "tasks.rsyslog") when rsyslog runs
// `mode: global` across multiple edge nodes (docker-stack.app.yml) - every
// node needs its ACL applied, not just whichever one currently holds the
// keepalived VIP.
func reloadRelayConfig() error {
	targetsHost := os.Getenv("RSYSLOG_RELOAD_TARGETS_HOST")
	if targetsHost == "" {
		url := os.Getenv("RSYSLOG_RELOAD_URL")
		if url == "" {
			url = "http://rsyslog:8082/cgi-bin/reload.sh"
		}
		return postReload(url)
	}

	port := os.Getenv("RSYSLOG_RELOAD_PORT")
	if port == "" {
		port = "8082"
	}

	ips, err := net.LookupHost(targetsHost)
	if err != nil {
		return fmt.Errorf("resolve rsyslog reload targets %q: %w", targetsHost, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("rsyslog reload targets %q resolved to no addresses", targetsHost)
	}

	type reloadResult struct {
		ip  string
		err error
	}
	results := make(chan reloadResult, len(ips))
	for _, ip := range ips {
		go func(ip string) {
			url := fmt.Sprintf("http://%s:%s/cgi-bin/reload.sh", ip, port)
			results <- reloadResult{ip: ip, err: postReload(url)}
		}(ip)
	}

	succeeded := 0
	var failures []string
	for i := 0; i < len(ips); i++ {
		r := <-results
		if r.err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", r.ip, r.err))
			continue
		}
		succeeded++
	}

	if succeeded == 0 {
		return fmt.Errorf("rsyslog reload failed on all %d target(s): %s", len(ips), strings.Join(failures, "; "))
	}
	if len(failures) > 0 {
		slog.Warn("rsyslog relay reload failed on some targets", "succeeded", succeeded, "total", len(ips), "errors", strings.Join(failures, "; "))
	}
	return nil
}

// SyncRelayConfig ensures the shared /data/relay PKI material exists,
// regenerates the IP allowlist from the current relay_ingestion_enabled
// setting and relay_whitelist table, and reloads rsyslog. Call whenever
// relay settings might have changed, and once at startup (see main.go) so a
// restart re-applies state that lives only in the non-git-tracked shared
// volume.
func SyncRelayConfig(database *sql.DB) error {
	if err := relaypki.EnsureCA(relayPKIDir()); err != nil {
		return fmt.Errorf("ensure relay CA: %w", err)
	}
	if err := writeRelayACL(database); err != nil {
		return err
	}
	return reloadRelayConfig()
}

// SyncRelayConfigWithRetry retries SyncRelayConfig a few times with a fixed
// delay, smoothing over the brief window where the rsyslog container's
// reload sidecar isn't listening yet (startup race, same as
// reloadNginxWithRetry).
func SyncRelayConfigWithRetry(database *sql.DB, attempts int, delay time.Duration) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = SyncRelayConfig(database); err == nil {
			return nil
		}
		if i < attempts-1 {
			time.Sleep(delay)
		}
	}
	return err
}

func ListRelayWhitelist(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		entries, err := db.GetRelayWhitelist(database)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to list relay whitelist", err))
			return
		}
		c.JSON(http.StatusOK, entries)
	}
}

func CreateRelayWhitelistEntry(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.RelayWhitelistRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Invalid request body", err))
			return
		}

		actorID, actorName := actorFromContext(c)
		entry, err := db.AddRelayWhitelistEntry(database, req.IPAddress, req.Label, nil, actorID)
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("Failed to add whitelist entry (IP may already be listed)", err))
			return
		}

		audit.LogAudit(database, actorID, actorName, "relay_whitelist_added", c.ClientIP(), fmt.Sprintf("ip=%s label=%s", req.IPAddress, req.Label))

		if err := SyncRelayConfig(database); err != nil {
			slog.Warn("relay config sync failed after whitelist add", "error", err)
			c.JSON(http.StatusCreated, gin.H{"entry": entry, "reload_error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, entry)
	}
}

// DeleteRelayWhitelistEntry removes an IP from the whitelist and, if it had
// a certificate linked, revokes that certificate too - a device that's no
// longer allowed in shouldn't leave an "issued" (i.e. still nominally
// active) certificate lying around. This is the one direction that's
// automatic; the reverse isn't (see RevokeRelayCertificate).
func DeleteRelayWhitelistEntry(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		entry, err := db.GetRelayWhitelistEntry(database, id)
		if err != nil && err != sql.ErrNoRows {
			middleware.HandleError(c, model.NewInternal("Failed to load whitelist entry", err))
			return
		}

		if err := db.DeleteRelayWhitelistEntry(database, id); err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to delete whitelist entry", err))
			return
		}

		actorID, actorName := actorFromContext(c)

		if entry != nil && entry.RelayCertID != nil {
			if err := db.RevokeRelayCertificate(database, *entry.RelayCertID); err != nil {
				slog.Warn("failed to revoke certificate after whitelist removal", "cert_id", *entry.RelayCertID, "error", err)
			} else {
				audit.LogAudit(database, actorID, actorName, "relay_certificate_revoked", c.ClientIP(),
					fmt.Sprintf("id=%d reason=whitelist_entry_removed", *entry.RelayCertID))
			}
		}

		audit.LogAudit(database, actorID, actorName, "relay_whitelist_removed", c.ClientIP(), fmt.Sprintf("id=%d", id))

		if err := SyncRelayConfig(database); err != nil {
			slog.Warn("relay config sync failed after whitelist delete", "error", err)
			c.JSON(http.StatusOK, gin.H{"message": "whitelist entry deleted", "reload_error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "whitelist entry deleted"})
	}
}

func ListRelayCertificates(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		certs, err := db.GetRelayCertificates(database)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to list relay certificates", err))
			return
		}
		c.JSON(http.StatusOK, certs)
	}
}

// issueRelayCertificate does the actual CA work shared by both ways of
// getting a new relay certificate (CreateRelayCertificate, which also
// whitelists a fresh IP, and GenerateCertificateForWhitelistEntry, which
// links it to an IP already whitelisted): make sure the CA exists, sign a
// client cert, and record its public metadata. Never touches
// relay_whitelist - callers do that themselves, since the two differ on
// insert vs. link.
func issueRelayCertificate(database *sql.DB, label string, actorID int64) (*relaypki.IssuedCert, int64, error) {
	if err := relaypki.EnsureCA(relayPKIDir()); err != nil {
		return nil, 0, fmt.Errorf("prepare relay CA: %w", err)
	}

	issued, err := relaypki.IssueClientCert(relayPKIDir(), label)
	if err != nil {
		return nil, 0, fmt.Errorf("issue relay certificate: %w", err)
	}

	certID, err := db.InsertRelayCertificate(database, label, issued.SerialHex, issued.Fingerprint, issued.ExpiresAt, actorID)
	if err != nil {
		return nil, 0, fmt.Errorf("record relay certificate: %w", err)
	}

	return issued, certID, nil
}

// respondWithCertificateBundle streams the one and only copy of a newly
// issued certificate's private key back to the caller, as a .tar.gz
// (ca.crt, client.crt, client.key, relay.conf). It also best-effort
// resyncs rsyslog's relay config - failures there are logged, not
// returned, since the cert/whitelist state is already committed by this
// point and the next sync (or the startup retry loop) picks it up anyway.
func respondWithCertificateBundle(c *gin.Context, database *sql.DB, issued *relaypki.IssuedCert, label string) {
	if err := SyncRelayConfig(database); err != nil {
		slog.Warn("relay config sync failed after certificate issue", "error", err)
	}

	bundle, err := buildRelayBundle(database, issued, label)
	if err != nil {
		middleware.HandleError(c, model.NewInternal("Failed to build certificate bundle", err))
		return
	}

	filename := fmt.Sprintf("syslog-relay-%s.tar.gz", sanitizeFilename(label))
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Data(http.StatusOK, "application/gzip", bundle)
}

// CreateRelayCertificate issues a new relay client certificate and, in the
// same step, whitelists its IP - then streams the only copy of the private
// key the admin will ever get. Fails with a bad request if the IP is
// already whitelisted (from a prior direct "Add IP", or an earlier
// certificate); use GenerateCertificateForWhitelistEntry for that case
// instead, to link a certificate onto the existing entry rather than
// trying to insert a second, rejected one.
func CreateRelayCertificate(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.RelayCertificateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Invalid request body", err))
			return
		}

		actorID, actorName := actorFromContext(c)

		issued, certID, err := issueRelayCertificate(database, req.Label, actorID)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to issue relay certificate", err))
			return
		}

		if _, err := db.AddRelayWhitelistEntry(database, req.IPAddress, req.Label, &certID, actorID); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Failed to whitelist relay IP - it may already be listed. If so, generate a certificate for that existing entry from the Whitelist IP tab instead.", err))
			return
		}

		audit.LogAudit(database, actorID, actorName, "relay_certificate_issued", c.ClientIP(),
			fmt.Sprintf("label=%s ip=%s serial=%s", req.Label, req.IPAddress, issued.SerialHex))

		respondWithCertificateBundle(c, database, issued, req.Label)
	}
}

// GenerateCertificateForWhitelistEntry issues a certificate for an IP
// that's already on the whitelist and links it to that entry, instead of
// CreateRelayCertificate's insert-a-new-entry path, which would be
// rejected by ip_address's unique constraint. Allowed both when the entry
// has no certificate yet and when its current one has been revoked (this
// is how an admin reissues after a revoke without leaving the Whitelist IP
// tab); blocked only while it still has an active ("issued") one.
func GenerateCertificateForWhitelistEntry(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		entry, err := db.GetRelayWhitelistEntry(database, id)
		if err != nil {
			if err == sql.ErrNoRows {
				middleware.HandleError(c, model.NewNotFound("Whitelist entry not found", nil))
				return
			}
			middleware.HandleError(c, model.NewInternal("Failed to load whitelist entry", err))
			return
		}
		if entry.RelayCertID != nil {
			status, err := db.GetRelayCertificateStatus(database, *entry.RelayCertID)
			if err != nil && err != sql.ErrNoRows {
				middleware.HandleError(c, model.NewInternal("Failed to check existing certificate", err))
				return
			}
			if status == model.RelayCertStatusIssued {
				middleware.HandleError(c, model.NewBadRequest("This whitelist entry already has an active certificate - revoke it first to issue a new one", nil))
				return
			}
		}

		actorID, actorName := actorFromContext(c)

		issued, certID, err := issueRelayCertificate(database, entry.Label, actorID)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to issue relay certificate", err))
			return
		}

		if err := db.LinkRelayCertificateToWhitelist(database, entry.ID, certID); err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to link certificate to whitelist entry", err))
			return
		}

		audit.LogAudit(database, actorID, actorName, "relay_certificate_issued", c.ClientIP(),
			fmt.Sprintf("label=%s ip=%s serial=%s whitelist_id=%d", entry.Label, entry.IPAddress, issued.SerialHex, entry.ID))

		respondWithCertificateBundle(c, database, issued, entry.Label)
	}
}

// RevokeRelayCertificate marks a certificate revoked and immediately shuts
// the relay out: writeRelayACL only admits IPs whose *currently linked*
// certificate is "issued", so a resync here regenerates allowed-relays.conf
// without this entry's IP and reloads rsyslog. This is a real cutoff, not
// just a database label - it doesn't depend on the relay's old private key
// becoming somehow invalid (there's no CRL/OCSP at the TLS layer), only on
// this server no longer admitting its IP.
//
// The whitelist entry itself is left alone (see db.RevokeRelayCertificate)
// - the device stays listed, shown as "Blocked" in the UI, until a new
// certificate is issued for it via GenerateCertificateForWhitelistEntry or
// RegenerateRelayCertificate.
func RevokeRelayCertificate(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		if err := db.RevokeRelayCertificate(database, id); err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to revoke relay certificate", err))
			return
		}

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "relay_certificate_revoked", c.ClientIP(), fmt.Sprintf("id=%d", id))

		if err := SyncRelayConfig(database); err != nil {
			slog.Warn("relay config sync failed after certificate revoke", "error", err)
			c.JSON(http.StatusOK, gin.H{"message": "certificate revoked", "reload_error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "certificate revoked"})
	}
}

// RegenerateRelayCertificate reissues a certificate for an existing one's
// whitelist entry, found by reverse lookup, so an admin can rotate
// credentials directly from the Certificates tab without hunting down the
// entry on Whitelist IP. It covers two cases (the frontend labels them
// "Regenerate" and "Renew" respectively, but it's the same operation):
//   - the certificate is already revoked - always allowed.
//   - the certificate is still "issued" but within
//     model.RelayCertRenewalWindowDays of its own expiry - allowed, and
//     the old one is revoked as part of the swap (see below) rather than
//     left "issued" right up until it actually expires.
//
// An "issued" certificate that isn't yet close to expiring is rejected;
// revoke it first if you need to replace it early.
//
// The old certificate row is always left in place for the audit trail.
// Requires the whitelist entry to still exist - if it was removed
// (DeleteRelayWhitelistEntry revokes but doesn't preserve a reverse link),
// re-add the IP on Whitelist IP first.
func RegenerateRelayCertificate(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseIDParam(c.Param("id"))
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("invalid id", nil))
			return
		}

		cert, err := db.GetRelayCertificate(database, id)
		if err != nil {
			if err == sql.ErrNoRows {
				middleware.HandleError(c, model.NewNotFound("Certificate not found", nil))
				return
			}
			middleware.HandleError(c, model.NewInternal("Failed to load certificate", err))
			return
		}

		nearingExpiry := time.Until(cert.ExpiresAt) <= model.RelayCertRenewalWindowDays*24*time.Hour
		if cert.Status == model.RelayCertStatusIssued && !nearingExpiry {
			middleware.HandleError(c, model.NewBadRequest(
				fmt.Sprintf("This certificate is still active and not within %d days of expiring - revoke it first to replace it early", model.RelayCertRenewalWindowDays), nil))
			return
		}

		entry, err := db.GetRelayWhitelistEntryByCertID(database, id)
		if err != nil {
			if err == sql.ErrNoRows {
				middleware.HandleError(c, model.NewBadRequest("No whitelist entry is linked to this certificate anymore - add the relay's IP on the Whitelist IP tab, then generate a certificate for it there", nil))
				return
			}
			middleware.HandleError(c, model.NewInternal("Failed to load linked whitelist entry", err))
			return
		}

		actorID, actorName := actorFromContext(c)

		issued, newCertID, err := issueRelayCertificate(database, entry.Label, actorID)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to issue relay certificate", err))
			return
		}

		if err := db.LinkRelayCertificateToWhitelist(database, entry.ID, newCertID); err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to link certificate to whitelist entry", err))
			return
		}

		if cert.Status == model.RelayCertStatusIssued {
			// Was still active (renewal path, not regenerate-after-revoke) -
			// now that its replacement is issued and linked, retire it
			// rather than leaving two "issued" rows for the same relay.
			if err := db.RevokeRelayCertificate(database, id); err != nil {
				slog.Warn("failed to revoke superseded certificate after renewal", "cert_id", id, "error", err)
			}
		}

		audit.LogAudit(database, actorID, actorName, "relay_certificate_issued", c.ClientIP(),
			fmt.Sprintf("label=%s ip=%s serial=%s whitelist_id=%d regenerated_from=%d", entry.Label, entry.IPAddress, issued.SerialHex, entry.ID, id))

		respondWithCertificateBundle(c, database, issued, entry.Label)
	}
}

func buildRelayBundle(database *sql.DB, issued *relaypki.IssuedCert, label string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	files := []struct {
		name string
		data []byte
	}{
		{"ca.crt", issued.CAPEM},
		{"client.crt", issued.CertPEM},
		{"client.key", issued.KeyPEM},
		{"relay.conf", relayConfSnippet(database, label)},
	}
	for _, f := range files {
		hdr := &tar.Header{Name: f.name, Mode: 0600, Size: int64(len(f.data))}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("tar header %s: %w", f.name, err)
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, fmt.Errorf("tar write %s: %w", f.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// relayConfSnippet is a ready-to-use rsyslog config for the relay side:
// listens on 514/tcp+udp like the standalone single-server rsyslog
// (rsyslog/syslog.conf), but forwards over mTLS instead of writing a local
// file. The target host is this server's address reachable from the
// relay's VLAN, resolved in order: the "relay_central_host" setting
// (Admin > Syslog Relay), then the RELAY_CENTRAL_HOST env var, then
// "127.0.0.1" - always a syntactically valid address rather than a
// placeholder the admin might forget to replace, but 127.0.0.1 only makes
// sense for same-host testing; a real cross-VLAN deployment needs one of
// the first two set.
func relayConfSnippet(database *sql.DB, label string) []byte {
	host := db.GetSetting(database, "relay_central_host", "")
	if host == "" {
		host = os.Getenv("RELAY_CENTRAL_HOST")
	}
	if host == "" {
		host = "127.0.0.1"
	}

	tpl := `# Generated for relay %q. Place ca.crt / client.crt / client.key from this
# bundle at /etc/syslog-relay/tls/ on the relay host, then use this file as
# rsyslog.d/relay.conf there. See README "Syslog Relay" for the full
# deployment steps (docker-compose.relay.yml).
#
# TLS material is set via the GLOBAL DefaultNetstreamDriver* params below,
# not the omfwd action's own StreamDriverCAFile/CertFile/KeyFile params:
# those per-action params only exist on the sender side starting in rsyslog
# 8.2310 (https://github.com/rsyslog/rsyslog/issues/5150) - on the 8.2302.0
# that ships in Debian bookworm (Dockerfile.rsyslog-relay's base image)
# they're not just ignored but rejected outright as unknown parameters
# ("typo in config file?"), so omfwd's action() below deliberately omits
# them and relies on this global() block instead.
global(
  DefaultNetstreamDriverCAFile="/etc/syslog-relay/tls/ca.crt"
  DefaultNetstreamDriverCertFile="/etc/syslog-relay/tls/client.crt"
  DefaultNetstreamDriverKeyFile="/etc/syslog-relay/tls/client.key"
)

module(load="imtcp")
input(type="imtcp" port="514")
module(load="imudp")
input(type="imudp" port="514")

template(name="JsonLines" type="list") {
  constant(value="{")
  constant(value="\"timestamp\":\"")
  # timegenerated (this relay's own receipt clock) instead of timereported
  # (parsed from the device's message body): most devices send legacy
  # RFC3164 timestamps with no timezone offset, which rsyslog would
  # otherwise fill in using ITS OWN local time - wrong whenever the relay
  # container's clock isn't in the same zone as the devices behind it, and
  # silently wrong by that offset (e.g. a relay defaulting to UTC while its
  # devices are in CEST reports every timestamp 2h ahead). timegenerated is
  # stamped at receipt from the system clock, which is always a correct UTC
  # instant regardless of the container's configured timezone - stamped
  # before this relay's own disk-buffered retry queue, so it isn't skewed by
  # delivery delays either. See README "Syslog Relay".
  property(name="timegenerated" dateFormat="rfc3339" format="json")
  constant(value="\",\"hostname\":\"")
  property(name="hostname" format="json")
  constant(value="\",\"fromhost_ip\":\"")
  property(name="fromhost-ip" format="json")
  constant(value="\",\"severity\":\"")
  property(name="syslogseverity-text" format="json")
  constant(value="\",\"facility\":\"")
  property(name="syslogfacility-text" format="json")
  constant(value="\",\"app_name\":\"")
  property(name="programname" format="json")
  constant(value="\",\"process_id\":\"")
  property(name="procid" format="json")
  constant(value="\",\"message\":\"")
  property(name="msg" format="json")
  constant(value="\",\"via_relay\":\"%s\"}\n")
}

*.* action(type="omfwd"
  target=%q port="6514" protocol="tcp"
  template="JsonLines"
  StreamDriver="gtls"
  StreamDriverMode="1"
  StreamDriverAuthMode="x509/certvalid"
  queue.type="LinkedList"
  queue.filename="relayqueue"
  queue.saveOnShutdown="on"
  queue.maxDiskSpace="1g"
  action.resumeRetryCount="-1"
  action.resumeInterval="10"
)
`
	return []byte(fmt.Sprintf(tpl, label, host, jsonEscapeForRsyslogConstant(label)))
}

// jsonEscapeForRsyslogConstant prepares label to sit inside a JSON string
// value that's itself embedded in an rsyslog constant(value="...") literal
// (see the "via_relay" field in relayConfSnippet's JsonLines template) -
// two escaping layers stacked on each other, since label ends up nested
// inside both. JSON-encode it first (quotes/backslashes/control chars
// become JSON escapes), then escape backslashes and quotes a second time so
// that JSON-escaped text survives rsyslog's own string literal parsing
// unchanged - otherwise a label containing a quote or backslash would
// either corrupt the generated config or the JSON it's meant to produce.
func jsonEscapeForRsyslogConstant(label string) string {
	jsonBytes, _ := json.Marshal(label)
	inner := string(jsonBytes[1 : len(jsonBytes)-1])
	inner = strings.ReplaceAll(inner, `\`, `\\`)
	inner = strings.ReplaceAll(inner, `"`, `\"`)
	return inner
}

var filenameUnsafeRe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitizeFilename(s string) string {
	s = filenameUnsafeRe.ReplaceAllString(s, "-")
	if s == "" {
		return "relay"
	}
	return s
}
