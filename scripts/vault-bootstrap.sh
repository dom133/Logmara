#!/usr/bin/env bash
#
# Bootstrap HashiCorp Vault cluster for Docker Swarm.
#
# Usage:
#   ./scripts/vault-bootstrap.sh init
#   ./scripts/vault-bootstrap.sh unseal
#   ./scripts/vault-bootstrap.sh migrate-secrets
#   ./scripts/vault-bootstrap.sh policy
#
# Subcommands:
#   init             Initialize the Vault cluster (Shamir unseal)
#   unseal           Unseal all 3 Vault nodes using Shamir keys
#   migrate-secrets  Migrate existing Docker secrets to Vault KV
#   policy           Create logmara policy for agent access
#

set -euo pipefail

ACTION="${1:-}"

VAULT_ADDR="http://vault-1:8200"

# Run vault CLI inside a container
vault_cli() {
    docker run --rm \
        --network syslog_net \
        -e VAULT_ADDR="$VAULT_ADDR" \
        -e VAULT_TOKEN="$VAULT_TOKEN" \
        vault:1.15.0 "$@"
}

# Write a secret to Vault KV v2 using the API (avoids shell escaping issues)
vault_kv_write() {
    local path="$1"
    local key="$2"
    local value="$3"
    local tmpfile
    tmpfile=$(mktemp)
    printf '%s' "$value" > "$tmpfile"
    docker run --rm \
        --network syslog_net \
        -e VAULT_ADDR="$VAULT_ADDR" \
        -e VAULT_TOKEN="$VAULT_TOKEN" \
        -v "$tmpfile:/tmp/secretval:ro" \
        vault:1.15.0 sh -c "
            VAL=\$(cat /tmp/secretval) && \
            vault kv put \"${path}\" \"${key}=\$VAL\"
        " 2>/dev/null || true
    rm -f "$tmpfile"
}

case "$ACTION" in
    init)
        echo "=== Initializing Vault cluster ==="

        # Wait for Vault to be ready
        echo "Waiting for Vault to be ready..."
        for i in {1..30}; do
            if docker run --rm --network syslog_net vault:1.15.0 \
                -e VAULT_ADDR="$VAULT_ADDR" \
                status 2>/dev/null | grep -q "sealed\|inactive"; then
                echo "Vault is ready"
                break
            fi
            echo "Waiting... ($i/30)"
            sleep 2
        done

        # Initialize with Shamir unseal (key_threshold=3, key_shares=5)
        echo "Initializing Vault with Shamir unseal (3/5 keys)..."
        INIT_OUTPUT=$(docker run --rm \
            --network syslog_net \
            -e VAULT_ADDR="$VAULT_ADDR" \
            vault:1.15.0 operator init \
                -key-shares=5 \
                -key-threshold=3 \
                -format=json 2>/dev/null)

        if [[ -z "$INIT_OUTPUT" ]]; then
            echo "ERROR: Vault initialization failed (may already be initialized)" >&2
            exit 1
        fi

        # Extract root token and unseal keys
        ROOT_TOKEN=$(echo "$INIT_OUTPUT" | jq -r '.root_token')
        echo "$INIT_OUTPUT" | jq -r '.unseal_keys_b64[]' > /tmp/vault_unseal_keys.txt

        echo "Root token: $ROOT_TOKEN"
        echo "Unseal keys saved to /tmp/vault_unseal_keys.txt"
        echo ""
        echo "IMPORTANT: Store these keys securely!"
        echo "You will need 3 of 5 keys to unseal Vault."

        # Store root token for subsequent automation (rotate-secrets.sh, etc.)
        mkdir -p /srv/syslog-ha
        echo "$ROOT_TOKEN" > /srv/syslog-ha/vault-token
        chmod 600 /srv/syslog-ha/vault-token

        # Set the root token for subsequent commands
        export VAULT_TOKEN="$ROOT_TOKEN"

        echo "=== Vault initialized ==="
        ;;

    unseal)
        echo "=== Unsealing Vault cluster ==="

        if [[ ! -f /tmp/vault_unseal_keys.txt ]]; then
            echo "ERROR: Unseal keys not found at /tmp/vault_unseal_keys.txt" >&2
            exit 1
        fi

        # Read unseal keys
        mapfile -t KEYS < /tmp/vault_unseal_keys.txt

        # Unseal each node
        for NODE in vault-1 vault-2 vault-3; do
            echo "Unsealing $NODE..."
            VAULT_ADDR="http://$NODE:8200"

            # Use 3 keys to unseal
            for i in 0 1 2; do
                docker run --rm \
                    --network syslog_net \
                    -e VAULT_ADDR="$VAULT_ADDR" \
                    vault:1.15.0 operator unseal "${KEYS[$i]}" 2>/dev/null || true
            done

            echo "$NODE unsealed"
        done

        echo "=== Vault cluster unsealed ==="
        ;;

    policy)
        echo "=== Creating logmara policy ==="

        if [[ -z "${VAULT_TOKEN:-}" ]]; then
            echo "ERROR: VAULT_TOKEN not set" >&2
            exit 1
        fi

        # Create policy for agent access
        vault_cli policy write logmara <<'EOF'
path "secret/data/logmara/*" {
  capabilities = ["read"]
}

path "secret/data/logmara/agent_token" {
  capabilities = ["read"]
}
EOF

        echo "=== Policy created ==="
        ;;

    migrate-secrets)
        echo "=== Migrating Docker secrets to Vault ==="

        if [[ -z "${VAULT_TOKEN:-}" ]]; then
            echo "ERROR: VAULT_TOKEN not set" >&2
            exit 1
        fi

        # Enable KV v2 secrets engine
        vault_cli secrets enable -path=secret kv-v2 2>/dev/null || true

        # Read existing Docker secrets and write to Vault
        for SECRET in jwt_secret encryption_key pg_app_password pg_superuser_password redis_password rabbitmq_password; do
            VALUE=$(docker run --rm --secret "$SECRET" alpine cat /run/secrets/"$SECRET" 2>/dev/null || echo "")
            if [[ -n "$VALUE" ]]; then
                echo "Migrating $SECRET..."
                vault_kv_write "secret/data/logmara/$SECRET" "value" "$VALUE"
            fi
        done

        # Create agent token
        AGENT_TOKEN=$(vault_cli token create \
            -policy=logmara \
            -ttl=24h \
            -period=24h \
            -format=json 2>/dev/null | jq -r '.auth.client_token')

        vault_kv_write "secret/data/logmara/agent_token" "token" "$AGENT_TOKEN"

        echo "=== Secrets migrated ==="
        ;;

    *)
        echo "Usage: $0 {init|unseal|policy|migrate-secrets}" >&2
        exit 1
        ;;
esac
