package ldap

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"

	ldaplib "github.com/go-ldap/ldap/v3"
)

type Config struct {
	Server        string
	Port          int
	BaseDN        string
	BindDN        string
	BindPassword  string
	UseTLS        bool
	VerifyCert    bool
	CaCert        string
	UserFilter    string
	Enabled       bool
	UsernameAttr  string
	EmailAttr     string
	DefaultRole   string
	AutoProvision bool
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
	usernameAttr := getSetting("ldap_username_attr", "uid")
	emailAttr := getSetting("ldap_email_attr", "mail")
	defaultRole := getSetting("ldap_default_role", "viewer")
	autoProvision := getSetting("ldap_auto_provision", "true") == "true"

	return &Config{
		Server:        server,
		Port:          port,
		BaseDN:        getSetting("ldap_base_dn", ""),
		BindDN:        getSetting("ldap_bind_dn", ""),
		BindPassword:  bindPassword,
		UseTLS:        useTLS,
		VerifyCert:    verifyCert,
		CaCert:        caCert,
		UserFilter:    userFilter,
		Enabled:       true,
		UsernameAttr:  usernameAttr,
		EmailAttr:     emailAttr,
		DefaultRole:   defaultRole,
		AutoProvision: autoProvision,
	}
}

func Authenticate(cfg *Config, username, password string) (map[string]string, error) {
	if !cfg.Enabled || cfg.Server == "" {
		return nil, nil
	}

	l, err := dialLDAP(cfg)
	if err != nil {
		slog.Error("ldap dial error", "error", err)
		return nil, err
	}
	defer l.Close()

	usernameAttr := cfg.UsernameAttr
	if usernameAttr == "" {
		usernameAttr = "uid"
	}
	emailAttr := cfg.EmailAttr
	if emailAttr == "" {
		emailAttr = "mail"
	}

	if cfg.BindDN != "" && cfg.BindPassword != "" {
		err = l.Bind(cfg.BindDN, cfg.BindPassword)
		if err != nil {
			slog.Error("ldap bind error", "error", err)
			return nil, err
		}

		// Escape the username before interpolating it into the search filter,
		// otherwise LDAP metacharacters (*, (, ), \, NUL) in a login let an
		// attacker alter the filter's meaning (LDAP injection).
		filter := fmt.Sprintf(cfg.UserFilter, ldaplib.EscapeFilter(username))
		searchReq := ldaplib.NewSearchRequest(
			cfg.BaseDN,
			ldaplib.ScopeWholeSubtree, ldaplib.NeverDerefAliases, 0, 0, false,
			filter,
			[]string{"dn", usernameAttr, emailAttr},
			nil,
		)

		sr, err := l.Search(searchReq)
		if err != nil {
			slog.Error("ldap search error", "error", err)
			return nil, err
		}

		if len(sr.Entries) == 0 {
			return nil, nil
		}

		userDN := sr.Entries[0].DN
		entry := sr.Entries[0]
		err = l.Bind(userDN, password)
		if err != nil {
			slog.Error("ldap auth error", "error", err)
			return nil, err
		}

		attrs := make(map[string]string)
		for _, attr := range entry.Attributes {
			if attr.Name == usernameAttr && len(attr.Values) > 0 {
				attrs["username"] = attr.Values[0]
			}
			if attr.Name == emailAttr && len(attr.Values) > 0 {
				attrs["email"] = attr.Values[0]
			}
		}

		return attrs, nil
	} else {
		err = l.Bind(username, password)
		if err != nil {
			slog.Error("ldap auth error", "error", err)
			return nil, err
		}
		return map[string]string{"username": username}, nil
	}
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
		ServerName:         cfg.Server,
	}

	if cfg.CaCert != "" && cfg.VerifyCert {
		caCertPool := x509.NewCertPool()
		if caCertPool.AppendCertsFromPEM([]byte(cfg.CaCert)) {
			tc.RootCAs = caCertPool
		}
	}

	return tc
}

func TestConnection(server string, port int, useTLS bool, verifyCert bool, caCert, baseDN, bindDN, bindPassword string) error {
	if server == "" {
		return fmt.Errorf("server is required")
	}

	cfg := &Config{
		Server:       server,
		Port:         port,
		UseTLS:       useTLS,
		VerifyCert:   verifyCert,
		CaCert:       caCert,
		BaseDN:       baseDN,
		BindDN:       bindDN,
		BindPassword: bindPassword,
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
