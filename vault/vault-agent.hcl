# Vault agent configuration.
#
# Reads secrets from Vault KV v2 and writes them to local files
# at /vault-agent/secrets/. The api service mounts this directory
# read-only and uses the *_FILE env var convention to read secrets.
#
# Auto-auth: token_file, reading a bootstrap token that Docker Swarm
# distributes to every node as the `vault_agent_token` secret (created by
# `scripts/vault-bootstrap.sh migrate-secrets`). It can't auto-auth by
# reading a Vault-stored template instead, since that would require an
# already-authenticated session to fetch the very token used to authenticate.

auto_auth {
  method "token_file" {
    config {
      token_file_path = "/run/secrets/vault_agent_token"
    }
  }
  sink "file" {
    config {
      path = "/vault-agent/token"
    }
  }
}

listener "tcp" {
  tls_disable = 1
  address = "0.0.0.0:8300"
}

cache {
  use_auto_auth_token = true
}

template {
  destination = "/vault-agent/secrets/jwt_secret"
  contents = <<EOF
{{ with secret "secret/data/logmara/jwt_secret" }}{{ .Data.data.value }}{{ end }}
EOF
}

template {
  destination = "/vault-agent/secrets/encryption_key"
  contents = <<EOF
{{ with secret "secret/data/logmara/encryption_key" }}{{ .Data.data.value }}{{ end }}
EOF
}

template {
  destination = "/vault-agent/secrets/redis_password"
  contents = <<EOF
{{ with secret "secret/data/logmara/redis_password" }}{{ .Data.data.value }}{{ end }}
EOF
}

template {
  destination = "/vault-agent/secrets/rabbitmq_password"
  contents = <<EOF
{{ with secret "secret/data/logmara/rabbitmq_password" }}{{ .Data.data.value }}{{ end }}
EOF
}

vault {
  # No load balancer in front of the 3-node cluster; vault-1 is a fixed
  # entry point (matches vault-bootstrap.sh / rotate-secrets.sh). Vault
  # transparently forwards to the current Raft leader if vault-1 isn't it.
  address = "http://vault-1:8200"
}
