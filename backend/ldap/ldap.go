package ldap

import (
	"crypto/tls"
	"fmt"
	"log"

	"github.com/go-ldap/ldap/v3"
)

type Config struct {
	Server       string
	Port         int
	BaseDN       string
	BindDN       string
	BindPassword string
	UseTLS       bool
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

	if port == 0 {
		port = 636
	}

	userFilter := getSetting("ldap_user_filter", "(uid=%s)")

	return &Config{
		Server:       server,
		Port:         port,
		BaseDN:       getSetting("ldap_base_dn", ""),
		BindDN:       getSetting("ldap_bind_dn", ""),
		BindPassword: getSetting("ldap_bind_password", ""),
		UseTLS:       useTLS,
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
		searchReq := ldap.NewSearchRequest(
			cfg.BaseDN,
			ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
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

func dialLDAP(cfg *Config) (*ldap.Conn, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Server, cfg.Port)

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}

	if cfg.UseTLS && cfg.Port == 636 {
		return ldap.DialTLS("tcp", addr, tlsConfig)
	}

	l, err := ldap.Dial("tcp", addr)
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