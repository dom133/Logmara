package util

import (
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
