package ldap

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"

	ldaplib "github.com/go-ldap/ldap/v3"
)

type Config struct {
	Server       string
	Port         int
	BaseDN       string
	BindDN       string
	BindPassword string
	UseTLS       bool
	VerifyCert   bool
	CaCert       string
	UserFilter   string
	Enabled      bool
}

func LoadConfig(getSetting func(string, string) string) *Config {
	enabled := false
	if v := getSetting("ldap_enabled", "false"); v == "true" {
		enabled = true
	}

	server := getSetting("ldap_server", "")
	if !enabled || server == "" {
		return &Config{Enabled: false}
	}

	port := 389
	if v := getSetting("ldap_port", "389"); v != "" {
		fmt.Sscanf(v, "%d", &port)
	}

	useTLS := false
	if v := getSetting("ldap_use_tls", "false"); v == "true" {
		useTLS = true
	}

	verifyCert := true
	if v := getSetting("ldap_verify_cert", "true"); v == "false" {
		verifyCert = false
	}

	caCert := getSetting("ldap_ca_cert", "")
	bindPassword := getSetting("ldap_bind_password", "")

	if port == 0 {
		port = 636
	}

	userFilter := getSetting("ldap_user_filter", "(uid=%s)")

	return &Config{
		Server:       server,
		Port:         port,
		BaseDN:       getSetting("ldap_base_dn", ""),
		BindDN:       getSetting("ldap_bind_dn", ""),
		BindPassword: bindPassword,
		UseTLS:       useTLS,
		VerifyCert:   verifyCert,
		CaCert:       caCert,
		UserFilter:   userFilter,
		Enabled:      true,
	}
}

func Authenticate(cfg *Config, username, password string) bool {
	if !cfg.Enabled || cfg.Server == "" {
		return false
	}

	l, err := dialLDAP(cfg)
	if err != nil {
		log.Printf("LDAP dial error: %v", err)
		return false
	}
	defer l.Close()

	if cfg.BindDN != "" && cfg.BindPassword != "" {
		err = l.Bind(cfg.BindDN, cfg.BindPassword)
		if err != nil {
			log.Printf("LDAP bind error: %v", err)
			return false
		}

		filter := fmt.Sprintf(cfg.UserFilter, username)
		searchReq := ldaplib.NewSearchRequest(
			cfg.BaseDN,
			ldaplib.ScopeWholeSubtree, ldaplib.NeverDerefAliases, 0, 0, false,
			filter,
			[]string{"dn"},
			nil,
		)

		sr, err := l.Search(searchReq)
		if err != nil {
			log.Printf("LDAP search error: %v", err)
			return false
		}

		if len(sr.Entries) == 0 {
			return false
		}

		userDN := sr.Entries[0].DN
		err = l.Bind(userDN, password)
	} else {
		err = l.Bind(username, password)
	}

	if err != nil {
		log.Printf("LDAP auth error: %v", err)
		return false
	}

	return true
}

func dialLDAP(cfg *Config) (*ldaplib.Conn, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Server, cfg.Port)

	tlsConfig := buildTLSConfig(cfg)

	if cfg.UseTLS && cfg.Port == 636 {
		return ldaplib.DialTLS("tcp", addr, tlsConfig)
	}

	l, err := ldaplib.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	if cfg.UseTLS {
		if err := l.StartTLS(tlsConfig); err != nil {
			l.Close()
			return nil, fmt.Errorf("StartTLS failed: %w", err)
		}
	}

	return l, nil
}

func buildTLSConfig(cfg *Config) *tls.Config {
	tc := &tls.Config{
		InsecureSkipVerify: !cfg.VerifyCert,
	}

	if cfg.CaCert != "" && cfg.VerifyCert {
		caCertPool := x509.NewCertPool()
		if caCertPool.AppendCertsFromPEM([]byte(cfg.CaCert)) {
			tc.RootCAs = caCertPool
		}
	}

	return tc
}

func TestConnection(server string, port int, useTLS bool, baseDN, bindDN, bindPassword string) error {
	if server == "" {
		return fmt.Errorf("server is required")
	}

	cfg := &Config{
		Server:       server,
		Port:         port,
		UseTLS:       useTLS,
		BaseDN:       baseDN,
		BindDN:       bindDN,
		BindPassword: bindPassword,
		VerifyCert:   true,
	}

	l, err := dialLDAP(cfg)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer l.Close()

	if bindDN != "" && bindPassword != "" {
		if err := l.Bind(bindDN, bindPassword); err != nil {
			return fmt.Errorf("bind failed: %w", err)
		}
	}

	return nil
}