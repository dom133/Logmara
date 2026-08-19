package auth

import (
	"crypto/rsa"
	"strings"
	"testing"
	"time"

	"logmara/util"

	"github.com/golang-jwt/jwt/v5"
)

// TestDualModeTokenRoundTrip verifies that with an RSA key configured, new
// tokens are signed with RS256 and validate, while a token signed with the
// symmetric HS256 secret (as issued before the upgrade) still validates - the
// dual-mode guarantee that lets the upgrade roll out without logging out.
func TestDualModeTokenRoundTrip(t *testing.T) {
	cfg := newTestConfigWithRSA(t)

	// New token should be RS256.
	tokenStr, _, _, err := cfg.GenerateToken(1, "alice", "admin", false)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if got := algOf(t, tokenStr); got != "RS256" {
		t.Fatalf("expected RS256 token, got alg=%s", got)
	}
	claims, err := cfg.ValidateToken(tokenStr)
	if err != nil {
		t.Fatalf("ValidateToken(RS256): %v", err)
	}
	if uid, _ := (*claims)["user_id"].(float64); int64(uid) != 1 {
		t.Fatalf("expected user_id 1, got %v", (*claims)["user_id"])
	}

	// An HS256 token (as issued before the upgrade) must still validate.
	hmacToken := signHS256(t, cfg.jwtSecretPrimary, 2, "bob")
	hclaims, err := cfg.ValidateToken(hmacToken)
	if err != nil {
		t.Fatalf("ValidateToken(HS256 legacy): %v", err)
	}
	if uid, _ := (*hclaims)["user_id"].(float64); int64(uid) != 2 {
		t.Fatalf("expected user_id 2, got %v", (*hclaims)["user_id"])
	}

	// A tampered signature must be rejected.
	if _, err := cfg.ValidateToken(tamperSignature(tokenStr)); err == nil {
		t.Fatalf("expected tampered RS256 token to be rejected")
	}
}

// TestRS256RotationGrace verifies that after RotateRSAKey, tokens signed with
// the old key still validate during the grace period, until the secondary key
// is cleared.
func TestRS256RotationGrace(t *testing.T) {
	oldPriv := genRSA(t)
	cfg := newTestConfigWithRSAKey(t, oldPriv)

	oldToken := signRS256(t, oldPriv, 7)

	cfg.RotateRSAKey(genRSA(t))

	// Old token still valid via the secondary public key.
	if _, err := cfg.ValidateToken(oldToken); err != nil {
		t.Fatalf("expected old RS256 token to validate during grace, got %v", err)
	}

	cfg.ClearSecondaryRSAKey()
	if _, err := cfg.ValidateToken(oldToken); err == nil {
		t.Fatalf("expected old RS256 token to be rejected after grace cleared")
	}
}

func genRSA(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	privPEM, _, err := util.GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("GenerateRSAKeyPair: %v", err)
	}
	priv, err := util.ParseRSAPrivateKey(privPEM)
	if err != nil {
		t.Fatalf("ParseRSAPrivateKey: %v", err)
	}
	return priv
}

func newTestConfigWithRSA(t *testing.T) *Config {
	return newTestConfigWithRSAKey(t, genRSA(t))
}

func newTestConfigWithRSAKey(t *testing.T, priv *rsa.PrivateKey) *Config {
	t.Helper()
	return &Config{
		jwtSecretPrimary: []byte("test-hmac-secret-test-hmac-secret-000"),
		rsaPriv:          priv,
		rsaPub:           priv.Public().(*rsa.PublicKey),
	}
}

func signRS256(t *testing.T, priv *rsa.PrivateKey, uid int64) string {
	t.Helper()
	claims := jwt.MapClaims{
		"user_id": uid,
		"jti":     "test-jti",
		"exp":     time.Now().Add(time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(priv)
	if err != nil {
		t.Fatalf("sign RS256: %v", err)
	}
	return s
}

func signHS256(t *testing.T, secret []byte, uid int64, username string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"user_id":  uid,
		"username": username,
		"jti":      "test-jti",
		"exp":      time.Now().Add(time.Hour).Unix(),
		"iat":      time.Now().Unix(),
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		t.Fatalf("sign HS256: %v", err)
	}
	return s
}

func algOf(t *testing.T, tokenStr string) string {
	t.Helper()
	tok, _, err := new(jwt.Parser).ParseUnverified(tokenStr, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("ParseUnverified: %v", err)
	}
	alg, _ := tok.Header["alg"].(string)
	return alg
}

// tamperSignature flips the first character of the JWT signature (a position
// that encodes significant bits, unlike the trailing base64 padding bits), so
// the decoded signature is guaranteed to change and verification must fail.
func tamperSignature(s string) string {
	dot := strings.LastIndex(s, ".")
	if dot < 0 || dot+1 >= len(s) {
		return s
	}
	i := dot + 1
	c := s[i]
	if c == 'A' {
		c = 'B'
	} else {
		c = 'A'
	}
	return s[:i] + string(c) + s[i+1:]
}
