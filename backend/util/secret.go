package util

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync/atomic"

	"logmara/vaultclient"
)

var secretLoadCounter atomic.Int64

func GetSecretLoadCount() int64 {
	return secretLoadCounter.Load()
}

// vaultSecretNames maps this app's env-var-style secret name to the path
// segment it's stored under in Vault (secret/logmara/<segment>, KV v2) -
// populated by `scripts/vault-bootstrap.sh migrate-secrets`, see
// docker-stack.vault.yml. Only secrets actually migrated there are listed;
// anything else always falls straight through to env/file below.
var vaultSecretNames = map[string]string{
	"JWT_SECRET":     "jwt_secret",
	"ENCRYPTION_KEY": "encryption_key",
	"REDIS_PASSWORD": "redis_password",
}

// SecretFromEnv reads a secret, in priority order:
//
//  1. Vault, if VAULT_ADDR is configured (see vaultclient) and this name is
//     one of vaultSecretNames - takes effect within vaultclient's cache TTL
//     of a scripts/rotate-secrets.sh rotation, no restart needed.
//  2. The plain environment variable name.
//  3. The file whose path is in name+"_FILE" (the Docker / Podman /
//     Kubernetes "*_FILE" secrets convention) - what a non-Vault Swarm /
//     Compose deployment mounts, and Vault's own fallback if unreachable.
//
// Surrounding whitespace and trailing newlines are trimmed from (2)/(3), so
// a secret file written with a trailing newline still yields the right
// value. Returns "" when none of the three is set.
//
// Reading secrets from Vault or a file mount is preferable to a plain env
// var in production: env vars leak through /proc, `docker inspect`, and
// crash dumps, whereas a file secret can be mounted read-only and kept off
// the process environment entirely, and a Vault read never touches disk.
func SecretFromEnv(name string) string {
	secretLoadCounter.Add(1)

	if vaultName, isVaulted := vaultSecretNames[name]; isVaulted {
		if v, ok := vaultclient.Get().GetSecret(vaultName); ok && v != "" {
			slog.Info("secret loaded", "name", name, "source", "vault")
			return v
		}
	}

	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		slog.Info("secret loaded", "name", name, "source", "env")
		return v
	}
	if p := strings.TrimSpace(os.Getenv(name + "_FILE")); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			slog.Info("secret loaded", "name", name, "source", "file")
			return strings.TrimSpace(string(b))
		}
	}
	slog.Warn("secret not loaded", "name", name, "source", "none")
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

// ResolveRabbitMQURL returns the RabbitMQ AMQP URL to use, in priority order:
//
//  1. RABBITMQ_URL / RABBITMQ_URL_FILE - the full AMQP URL as-is.
//  2. Built from RABBITMQ_HOST (required), RABBITMQ_PORT (default 5672),
//     RABBITMQ_USER (default logmara), and RABBITMQ_PASS / RABBITMQ_PASS_FILE
//     for the password. Lets a Swarm deployment mount the rabbitmq_password
//     secret into the api service.
//
// Returns "" when neither is configured (pre-RabbitMQ case, tailer falls
// back to local ingestion).
func ResolveRabbitMQURL() string {
	if dsn := SecretFromEnv("RABBITMQ_URL"); dsn != "" {
		return dsn
	}

	host := strings.TrimSpace(os.Getenv("RABBITMQ_HOST"))
	if host == "" {
		return ""
	}
	port := strings.TrimSpace(os.Getenv("RABBITMQ_PORT"))
	if port == "" {
		port = "5672"
	}
	user := strings.TrimSpace(os.Getenv("RABBITMQ_USER"))
	if user == "" {
		user = "logmara"
	}
	password := SecretFromEnv("RABBITMQ_PASS")

	return fmt.Sprintf("amqp://%s:%s@%s:%s", url.PathEscape(user), url.PathEscape(password), host, port)
}
