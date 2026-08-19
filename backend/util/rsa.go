package util

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// MinRSAKeyBits is the minimum RSA key size accepted for JWT_PRIVATE_KEY
// (RS256 signing). 2048 bits is NIST's current floor for RSA.
const MinRSAKeyBits = 2048

// ParseRSAPrivateKey parses an RSA private key from PEM, accepting both
// PKCS#8 ("PRIVATE KEY") and PKCS#1 ("RSA PRIVATE KEY") encodings. Used to
// load JWT_PRIVATE_KEY for RS256 signing. The key is validated to be at least
// MinRSAKeyBits long.
func ParseRSAPrivateKey(pemString string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemString))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in JWT_PRIVATE_KEY")
	}
	var key *rsa.PrivateKey
	switch block.Type {
	case "PRIVATE KEY":
		pk, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#8 private key: %w", err)
		}
		rsaKey, ok := pk.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("JWT_PRIVATE_KEY is not an RSA key")
		}
		key = rsaKey
	case "RSA PRIVATE KEY":
		pk, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#1 private key: %w", err)
		}
		key = pk
	default:
		return nil, fmt.Errorf("unsupported PEM block type %q (expected PRIVATE KEY or RSA PRIVATE KEY)", block.Type)
	}
	if key.N.BitLen() < MinRSAKeyBits {
		return nil, fmt.Errorf("JWT_PRIVATE_KEY is too small (%d bits); use at least %d bits", key.N.BitLen(), MinRSAKeyBits)
	}
	return key, nil
}

// GenerateRSAKeyPair generates a 2048-bit RSA key pair and returns the
// private key as PKCS#8 PEM (suitable for JWT_PRIVATE_KEY) and the public
// key as PKIX PEM (informational - the public key is derivable from the
// private one, so only the private key needs to be stored).
func GenerateRSAKeyPair() (privatePEM string, publicPEM string, err error) {
	key, err := rsa.GenerateKey(rand.Reader, MinRSAKeyBits)
	if err != nil {
		return "", "", fmt.Errorf("generate RSA key: %w", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("marshal PKCS#8 private key: %w", err)
	}
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))

	pubDER, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return "", "", fmt.Errorf("marshal PKIX public key: %w", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	return privPEM, pubPEM, nil
}
