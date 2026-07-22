// Package relaypki implements a minimal internal CA used to authenticate
// remote syslog relays (see backend/handler/relay.go) over mTLS: the
// central server signs its own CA on first use, signs a server cert for
// its rsyslog mTLS listener with it, and signs one client cert per relay
// on demand, giving each issuance a CommonName unique to that one cert
// (label + serial) - see IssueClientCert. There is no external PKI
// dependency and no CRL/OCSP - a relay cert is cut off either by its exact
// CommonName no longer being in the mTLS listener's PermittedPeer list
// (regenerated on every issue/revoke, see handler.writeRelayACL - this is
// what makes a regenerated certificate's old key stop working even though
// it's still cryptographically valid and CA-signed) or by removing its IP
// from the whitelist ACL entirely (see model.RelayCertStatusRevoked).
package relaypki

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	caCertFile     = "ca.crt"
	caKeyFile      = "ca.key"
	serverCertFile = "server.crt"
	serverKeyFile  = "server.key"

	caValidityYears     = 15
	serverValidityYears = 10
	clientValidityYears = 5

	// caRenewalWindow/serverRenewalWindow: how long before its own NotAfter
	// EnsureCA proactively re-signs a cert, so a deployment that's merely
	// restarted or touched periodically (see handler.SyncRelayConfig, called
	// on every relay change and at every startup) never actually reaches
	// expiry. Wide windows because both are long-lived and this is checked
	// far more often than either one changes.
	caRenewalWindow     = 365 * 24 * time.Hour
	serverRenewalWindow = 90 * 24 * time.Hour
)

func newSerialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

// EnsureCA makes sure dir contains a CA keypair and a server certificate
// signed by it (consumed by the rsyslog mTLS listener's
// DefaultNetstreamDriverCertFile/KeyFile), generating whichever is missing,
// and transparently renews either one in place once it's within its
// renewal window of expiring - see renewCA and issueServerCert. Safe to
// call on every request that touches relay config (and is - see
// handler.SyncRelayConfig): a no-op once both exist and aren't close to
// expiry.
func EnsureCA(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create pki dir: %w", err)
	}

	caCertPath := filepath.Join(dir, caCertFile)
	caKeyPath := filepath.Join(dir, caKeyFile)

	var caCert *x509.Certificate
	var caKey *rsa.PrivateKey

	if _, err := os.Stat(caCertPath); err == nil {
		caCert, caKey, err = loadCA(dir)
		if err != nil {
			return fmt.Errorf("load existing CA: %w", err)
		}
		if time.Until(caCert.NotAfter) <= caRenewalWindow {
			caCert, err = renewCA(dir, caCert, caKey)
			if err != nil {
				return fmt.Errorf("renew CA: %w", err)
			}
		}
	} else {
		caCert, caKey, err = generateCA()
		if err != nil {
			return fmt.Errorf("generate CA: %w", err)
		}
		if err := writeCertPEM(caCertPath, caCert.Raw, 0644); err != nil {
			return err
		}
		if err := writeRSAKeyPEM(caKeyPath, caKey, 0600); err != nil {
			return err
		}
	}

	serverCertPath := filepath.Join(dir, serverCertFile)
	if _, err := os.Stat(serverCertPath); err == nil {
		serverCert, err := loadCertOnly(serverCertPath)
		if err == nil && time.Until(serverCert.NotAfter) > serverRenewalWindow {
			return nil
		}
		// Missing/unparseable/expiring - fall through and reissue below,
		// same code path as "never existed".
	}
	return issueServerCert(dir, caCert, caKey)
}

func generateCA() (*x509.Certificate, *rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}
	cert, err := selfSignCA(key, pkix.Name{CommonName: "Syslytics Relay CA"})
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// renewCA re-signs a fresh self-signed CA certificate using the SAME
// private key and Subject as oldCert, just with NotBefore reset to now and
// NotAfter pushed out another caValidityYears - then overwrites ca.crt.
//
// This is deliberately not a real key rotation. TLS chain verification
// only needs the issuer's public key (to check the leaf's signature) and a
// currently-valid, name-matching trust anchor - not the exact certificate
// object presented at signing time - so a relay certificate signed years
// ago under the old ca.crt keeps validating against the new one without
// being reissued or redistributed, because both carry the same key and
// Subject. This only handles ordinary expiry; a suspected key compromise
// needs a real rotation instead (a new key, and every relay certificate
// reissued), which isn't automatic.
func renewCA(dir string, oldCert *x509.Certificate, key *rsa.PrivateKey) (*x509.Certificate, error) {
	cert, err := selfSignCA(key, oldCert.Subject)
	if err != nil {
		return nil, err
	}
	if err := writeCertPEM(filepath.Join(dir, caCertFile), cert.Raw, 0644); err != nil {
		return nil, err
	}
	return cert, nil
}

func selfSignCA(key *rsa.PrivateKey, subject pkix.Name) (*x509.Certificate, error) {
	serial, err := newSerialNumber()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               subject,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(caValidityYears, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// issueServerCert signs a fresh certificate (new key each time) for the
// central rsyslog mTLS listener and writes it to dir, overwriting whatever
// was there - used both the first time EnsureCA runs and to renew it once
// it's nearing expiry. Unlike the CA, there's no reason to keep the same
// key across reissuance: this cert is never distributed to anything that
// pins it, relays only need to trust the CA that signs it.
func issueServerCert(dir string, caCert *x509.Certificate, caKey *rsa.PrivateKey) error {
	serverKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("generate server key: %w", err)
	}
	serial, err := newSerialNumber()
	if err != nil {
		return fmt.Errorf("generate server serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "syslog-relay-central"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(serverValidityYears, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("sign server cert: %w", err)
	}
	if err := writeCertPEM(filepath.Join(dir, serverCertFile), der, 0644); err != nil {
		return err
	}
	return writeRSAKeyPEM(filepath.Join(dir, serverKeyFile), serverKey, 0600)
}

func loadCA(dir string) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, caCertFile))
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, caKeyFile))
	if err != nil {
		return nil, nil, err
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("invalid CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("invalid CA key PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	return cert, key, nil
}

// loadCertOnly reads just a certificate's expiry (no key needed) - used to
// decide whether the server cert needs renewing without also having to
// load its private key.
func loadCertOnly(path string) (*x509.Certificate, error) {
	certPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("invalid certificate PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

// writeAtomic writes data to a temp file in the same directory and renames
// it into place, so a concurrent reader (Go's own os.Stat existence check,
// or rsyslog's entrypoint.sh doing the same dance for its placeholder CA on
// first boot - both processes share /data over the same volume) never
// observes a partially-written cert/key file.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp, path, err)
	}
	return nil
}

func writeCertPEM(path string, der []byte, mode os.FileMode) error {
	return writeAtomic(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), mode)
}

func writeRSAKeyPEM(path string, key *rsa.PrivateKey, mode os.FileMode) error {
	der := x509.MarshalPKCS1PrivateKey(key)
	return writeAtomic(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}), mode)
}

// IssuedCert holds the one-time material for a newly issued relay client
// certificate. Unlike the CA/server cert, none of this is ever written to
// disk - CertPEM/KeyPEM/CAPEM live only in memory for the duration of the
// HTTP response that hands them to the admin, per the "download once"
// requirement (see handler.CreateRelayCertificate).
type IssuedCert struct {
	CertPEM     []byte
	KeyPEM      []byte
	CAPEM       []byte
	SerialHex   string
	Fingerprint string
	ExpiresAt   time.Time
}

// IssueClientCert signs a new client certificate for a relay identified by
// label (used as the cert's CommonName). dir must already contain a CA -
// call EnsureCA first.
func IssueClientCert(dir, label string) (*IssuedCert, error) {
	caCert, caKey, err := loadCA(dir)
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}
	caPEM, err := os.ReadFile(filepath.Join(dir, caCertFile))
	if err != nil {
		return nil, fmt.Errorf("read CA certificate: %w", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, fmt.Errorf("generate client key: %w", err)
	}
	serial, err := newSerialNumber()
	if err != nil {
		return nil, fmt.Errorf("generate client serial: %w", err)
	}
	// CommonName embeds the serial so this issuance's CN can never collide
	// with a previous (or future) one for the same label - the mTLS
	// listener's PermittedPeer is pinned to this exact string per
	// currently-"issued" certificate (see handler.writeRelayACL), so a
	// regenerated or revoked certificate's old CN simply stops matching
	// once its whitelist entry is relinked, rather than continuing to
	// authenticate as any CA-signed cert would under x509/certvalid.
	commonName := label + "#" + serial.Text(16)
	notAfter := time.Now().AddDate(clientValidityYears, 0, 0)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("sign client cert: %w", err)
	}
	keyDER := x509.MarshalPKCS1PrivateKey(key)

	fp := sha256.Sum256(der)

	return &IssuedCert{
		CertPEM:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:      pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER}),
		CAPEM:       caPEM,
		SerialHex:   serial.Text(16),
		Fingerprint: hex.EncodeToString(fp[:]),
		ExpiresAt:   notAfter,
	}, nil
}
