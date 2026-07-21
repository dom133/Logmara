// Package relaypki implements a minimal internal CA used to authenticate
// remote syslog relays (see backend/handler/relay.go) over mTLS: the
// central server signs its own CA on first use, signs a server cert for
// its rsyslog mTLS listener with it, and signs one client cert per relay
// on demand. There is no external PKI dependency and no CRL/OCSP - relay
// certs are cut off by removing their IP from the whitelist ACL instead
// (see model.RelayCertStatusRevoked).
package relaypki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"math/big"
)

const (
	caCertFile     = "ca.crt"
	caKeyFile      = "ca.key"
	serverCertFile = "server.crt"
	serverKeyFile  = "server.key"
)

func newSerialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

// EnsureCA makes sure dir contains a CA keypair and a server certificate
// signed by it (consumed by the rsyslog mTLS listener's
// DefaultNetstreamDriverCertFile/KeyFile), generating whichever is missing.
// Safe to call on every request that touches relay config - a no-op once
// both already exist.
func EnsureCA(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create pki dir: %w", err)
	}

	caCertPath := filepath.Join(dir, caCertFile)
	caKeyPath := filepath.Join(dir, caKeyFile)

	var caCert *x509.Certificate
	var caKey *ecdsa.PrivateKey

	if _, err := os.Stat(caCertPath); err == nil {
		caCert, caKey, err = loadCA(dir)
		if err != nil {
			return fmt.Errorf("load existing CA: %w", err)
		}
	} else {
		caCert, caKey, err = generateCA()
		if err != nil {
			return fmt.Errorf("generate CA: %w", err)
		}
		if err := writeCertPEM(caCertPath, caCert.Raw, 0644); err != nil {
			return err
		}
		if err := writeECKeyPEM(caKeyPath, caKey, 0600); err != nil {
			return err
		}
	}

	serverCertPath := filepath.Join(dir, serverCertFile)
	serverKeyPath := filepath.Join(dir, serverKeyFile)
	if _, err := os.Stat(serverCertPath); err == nil {
		return nil
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("sign server cert: %w", err)
	}
	if err := writeCertPEM(serverCertPath, der, 0644); err != nil {
		return err
	}
	return writeECKeyPEM(serverKeyPath, serverKey, 0600)
}

func generateCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := newSerialNumber()
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:                pkix.Name{CommonName: "Syslytics Relay CA"},
		NotBefore:              time.Now().Add(-time.Hour),
		NotAfter:               time.Now().AddDate(15, 0, 0),
		KeyUsage:               x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid:  true,
		IsCA:                   true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func loadCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
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
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	return cert, key, nil
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

func writeECKeyPEM(path string, key *ecdsa.PrivateKey, mode os.FileMode) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	return writeAtomic(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), mode)
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

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate client key: %w", err)
	}
	serial, err := newSerialNumber()
	if err != nil {
		return nil, fmt.Errorf("generate client serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: label},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(5, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("sign client cert: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal client key: %w", err)
	}

	fp := sha256.Sum256(der)

	return &IssuedCert{
		CertPEM:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:      pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		CAPEM:       caPEM,
		SerialHex:   serial.Text(16),
		Fingerprint: hex.EncodeToString(fp[:]),
	}, nil
}
