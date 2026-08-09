# Vault agent configuration.
#
# Reads secrets from Vault KV v2 and writes them to local files
# at /vault-agent/secrets/. The api service mounts this directory
# read-only and uses the *_FILE env var convention to read secrets.
#
# Auto-auth: token-based (bootstrap token set during vault-bootstrap.sh)

auto_auth {
  method "token" {
    config {
      token = "{{ with secret \"secret/data/logmara/agent_token\" }}{{ .Data.data.token }}{{ end }}"
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
  destination = "/vault-agent/secrets/pg_app_password"
  contents = <<EOF
{{ with secret "secret/data/logmara/pg_app_password" }}{{ .Data.data.value }}{{ end }}
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
  address = "http://vault:8200"
}
