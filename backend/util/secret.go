package util

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// SecretFromEnv reads a secret from the environment variable name, or - if
// that is unset - from the file whose path is in name+"_FILE" (the Docker /
// Podman / Kubernetes "*_FILE" secrets convention). Surrounding whitespace and
// trailing newlines are trimmed, so a secret file written with a trailing
// newline still yields the right value. Returns "" when neither is set.
//
// Reading secrets from a file mount is preferable to a plain env var in
// production: env vars leak through /proc, `docker inspect`, and crash dumps,
// whereas a file secret can be mounted read-only and kept off the process
// environment entirely.
func SecretFromEnv(name string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	if p := strings.TrimSpace(os.Getenv(name + "_FILE")); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

// ResolveDatabaseURL returns the Postgres connection string to use, in
// priority order:
//
//  1. DATABASE_URL / DATABASE_URL_FILE - the full DSN as-is (simplest path,
//     what docker-compose.yml and docker-stack.app.yml use today).
//  2. Built from POSTGRES_HOST plus POSTGRES_PORT/USER/DB (each optional,
//     with sane defaults) and POSTGRES_PASSWORD / POSTGRES_PASSWORD_FILE for
//     the password - lets a Swarm deployment mount the *same*
//     pg_app_password secret already used by docker-stack.postgres.yml into
//     the api service, instead of duplicating the password as a plain
//     DATABASE_URL env var.
//
// Returns "" when neither is configured (the pre-database setup wizard case).
func ResolveDatabaseURL() string {
	if dsn := SecretFromEnv("DATABASE_URL"); dsn != "" {
		return dsn
	}

	host := strings.TrimSpace(os.Getenv("POSTGRES_HOST"))
	if host == "" {
		return ""
	}
	port := strings.TrimSpace(os.Getenv("POSTGRES_PORT"))
	if port == "" {
		port = "5432"
	}
	user := strings.TrimSpace(os.Getenv("POSTGRES_USER"))
	if user == "" {
		user = "syslog"
	}
	dbname := strings.TrimSpace(os.Getenv("POSTGRES_DB"))
	if dbname == "" {
		dbname = "syslog_db"
	}
	sslmode := strings.TrimSpace(os.Getenv("POSTGRES_SSLMODE"))
	if sslmode == "" {
		sslmode = "disable"
	}
	password := SecretFromEnv("POSTGRES_PASSWORD")

	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, password),
		Host:     fmt.Sprintf("%s:%s", host, port),
		Path:     "/" + dbname,
		RawQuery: "sslmode=" + sslmode,
	}
	return u.String()
}
