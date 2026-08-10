#!/usr/bin/env bash
#
# Bootstrap HashiCorp Vault cluster for Docker Swarm.
#
# Usage:
#   ./scripts/vault-bootstrap.sh init
#   ./scripts/vault-bootstrap.sh unseal
#   ./scripts/vault-bootstrap.sh migrate-secrets
#   ./scripts/vault-bootstrap.sh policy
#   ./scripts/vault-bootstrap.sh setup-dynamic-secrets
#
# Subcommands:
#   init                     Initialize the Vault cluster (Shamir unseal)
#   unseal                   Unseal all 3 Vault nodes using Shamir keys
#   migrate-secrets          Migrate existing Docker secrets to Vault KV
#   policy                   Create logmara policy for agent access
#   setup-dynamic-secrets    Configure dynamic secrets engines (PG, Redis, RabbitMQ)
#

set -euo pipefail

ACTION="${1:-}"

VAULT_ADDR="http://vault-1:8200"

# Run vault CLI inside a container. -i keeps stdin open and forwards it
# into the container (needed for `policy write NAME -` reading a heredoc);
# harmless for the other subcommands this is used for, which don't read
# stdin at all.
vault_cli() {
    docker run --rm -i \
        --network syslog_net \
        -e VAULT_ADDR="$VAULT_ADDR" \
        -e VAULT_TOKEN="$VAULT_TOKEN" \
        hashicorp/vault:1.16.0 "$@"
}

# Write a secret to Vault KV v2. NOTE: `vault kv put/get` already prepends
# "data/" internally for a kv-v2 mount, so `path` here must be the display
# path (e.g. "secret/logmara/foo"), NOT the raw API path - passing the raw
# "secret/data/logmara/foo" path double-prepends and stores it one level
# too deep.
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
        hashicorp/vault:1.16.0 sh -c "
            VAL=\$(cat /tmp/secretval) && \
            vault kv put \"${path}\" \"${key}=\$VAL\"
        " 2>/dev/null || true
    rm -f "$tmpfile"
}

# Read the plaintext value of an existing Docker (Swarm) secret. `docker
# run --secret` is not a real flag - secrets are only mountable by Swarm
# services - so this spins up a one-shot, non-restarting service to read
# it via `docker service logs` and then removes it.
read_docker_secret_value() {
    local name="$1"
    local svc="vault-bootstrap-read-$$-$RANDOM"
    # --detach: don't wait for the service to converge here - some Docker
    # versions block on that by default (e.g. if the node scheduled to
    # pull `alpine` is slow/unreachable), and we already poll for the
    # result ourselves below.
    docker service create --quiet --detach --name "$svc" \
        --secret "$name" \
        --restart-condition none \
        --network syslog_net \
        alpine cat "/run/secrets/$name" >/dev/null 2>&1 || true
    local value=""
    for _ in $(seq 1 15); do
        # docker service logs prefixes every line with "<service>.<slot>.<taskid>@<node> | "
        # by default - strip it, or that prefix ends up baked into the secret value.
        value="$(docker service logs "$svc" 2>/dev/null | sed -E 's/^[^|]*\| ?//' | tr -d '\r\n')"
        [[ -n "$value" ]] && break
        sleep 1
    done
    docker service rm "$svc" >/dev/null 2>&1 || true
    printf '%s' "$value"
}

case "$ACTION" in
    init)
        echo "=== Initializing Vault cluster ==="

        # Wait for Vault to be ready
        echo "Waiting for Vault to be ready..."
        for i in {1..30}; do
            # `vault status` intentionally exits non-zero when sealed (2) -
            # that's the normal, expected state we're checking for here,
            # not a failure. With `set -o pipefail` (see top of file), that
            # exit code would otherwise win over grep's success and make
            # this `if` always false. The `|| true` inside the subshell
            # keeps only grep's match result significant to the pipe.
            if (docker run --rm --network syslog_net \
                -e VAULT_ADDR="$VAULT_ADDR" \
                hashicorp/vault:1.16.0 status 2>/dev/null || true) | grep -qi "sealed\|inactive"; then
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
            hashicorp/vault:1.16.0 operator init \
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
                    hashicorp/vault:1.16.0 operator unseal "${KEYS[$i]}" 2>/dev/null || true
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

        # Create policy for agent access. Raw KV v2 data paths need the
        # literal "data/" segment here (ACL policies always use the raw
        # API path, unlike the `vault kv` CLI subcommands).
        vault_cli policy write logmara - <<'EOF'
path "secret/data/logmara/*" {
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
            VALUE=$(read_docker_secret_value "$SECRET")
            if [[ -n "$VALUE" ]]; then
                echo "Migrating $SECRET..."
                vault_kv_write "secret/logmara/$SECRET" "value" "$VALUE"
            fi
        done

        # Create the Vault agent's bootstrap token and distribute it as a
        # Docker secret (Swarm hands it to every node's vault-agent task at
        # /run/secrets/vault_agent_token). It is NOT stored in Vault KV:
        # the agent needs this token to authenticate to Vault in the first
        # place, so storing it as a Vault secret would be circular.
        echo "Creating agent bootstrap token..."
        AGENT_TOKEN=$(vault_cli token create \
            -policy=logmara \
            -ttl=24h \
            -period=24h \
            -format=json 2>/dev/null | jq -r '.auth.client_token')

        if [[ -z "$AGENT_TOKEN" || "$AGENT_TOKEN" == "null" ]]; then
            echo "ERROR: Failed to create agent token" >&2
            exit 1
        fi

        if docker secret inspect vault_agent_token &>/dev/null; then
            echo "Docker secret 'vault_agent_token' already exists - leaving it as-is."
            echo "(Vault agent tokens can't be rotated in place; to replace it, remove"
            echo " the secret and the vault-agent service, then re-run this command.)"
        else
            printf '%s' "$AGENT_TOKEN" | docker secret create vault_agent_token -
            echo "Docker secret 'vault_agent_token' created."
        fi

        echo "=== Secrets migrated ==="
        echo "Now deploy the vault-agent stack (its secret didn't exist until now):"
        echo "  docker stack deploy -c docker-stack.vault-agent.yml logmara-vault-agent"
        ;;

    setup-dynamic-secrets)
        echo "=== Setting up dynamic secrets engines ==="

        if [[ -z "${VAULT_TOKEN:-}" ]]; then
            echo "ERROR: VAULT_TOKEN not set" >&2
            exit 1
        fi

        # Enable PostgreSQL dynamic secrets engine
        echo "Enabling PostgreSQL dynamic secrets engine..."
        vault_cli secrets enable -path=secret-dynamic/database database 2>/dev/null || true
        vault_cli write secret-dynamic/database/config/db \
            plugin_name=postgresql-database-plugin \
            allowed_roles="logmara-app" \
            connection_url="postgresql://${PG_SUPERUSER:-vault}:${PG_SUPERUSER_PASSWORD}@postgres:5432/${PG_DB:-syslog_db}?sslmode=disable" \
            username="${PG_SUPERUSER:-vault}" \
            password="${PG_SUPERUSER_PASSWORD}" 2>/dev/null || true

        # Create role for application user
        vault_cli write secret-dynamic/database/roles/logmara-app \
            db_name=db \
            creation_statements="CREATE ROLE \"{{name}}\" WITH LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}'; GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO \"{{name}}\";" \
            default_ttl="24h" \
            max_ttl="48h" 2>/dev/null || true

        # Enable Redis dynamic secrets engine (requires custom plugin)
        echo "Enabling Redis dynamic secrets engine..."
        vault_cli secrets enable -path=secret-dynamic/redis plugin 2>/dev/null || true
        vault_cli write sys/plugins/catalog/secret/redis \
            command=vault-plugin-secrets-redis \
            sha256=$(docker run --rm hashicorp/vault:1.16.0 sh -c 'vault plugin list 2>/dev/null | grep redis | awk "{print \$2}"') 2>/dev/null || true
        vault_cli write secret-dynamic/redis/config/conn \
            address=redis:6379 \
            password="${REDIS_PASSWORD:-}" 2>/dev/null || true
        vault_cli write secret-dynamic/redis/config/allow_role_creation_as_any_user \
            value=true 2>/dev/null || true
        vault_cli write secret-dynamic/redis/roles/logmara-app \
            db_num=0 \
            default_ttl="24h" \
            max_ttl="48h" 2>/dev/null || true

        # Enable RabbitMQ dynamic secrets engine (built-in in Vault 1.16)
        echo "Enabling RabbitMQ dynamic secrets engine..."
        vault_cli secrets enable -path=secret-dynamic/rabbitmq rabbitmq 2>/dev/null || true
        vault_cli write secret-dynamic/rabbitmq/config/connection \
            url="amqp://${RABBITMQ_DEFAULT_USER:-admin}:${RABBITMQ_DEFAULT_PASS}@rabbitmq:5672" \
            username="${RABBITMQ_DEFAULT_USER:-admin}" \
            password="${RABBITMQ_DEFAULT_PASS}" 2>/dev/null || true
        vault_cli write secret-dynamic/rabbitmq/roles/logmara-app \
            vhost="/" \
            tags="administrator,management" \
            default_ttl="24h" \
            max_ttl="48h" 2>/dev/null || true

        # Update policy to allow dynamic secret reads
        vault_cli policy write logmara-dynamic - <<'EOF'
path "secret-dynamic/database/*" {
  capabilities = ["read"]
}
path "secret-dynamic/redis/*" {
  capabilities = ["read"]
}
path "secret-dynamic/rabbitmq/*" {
  capabilities = ["read"]
}
EOF

        echo "=== Dynamic secrets engines configured ==="
        ;;

    *)
        echo "Usage: $0 {init|unseal|policy|migrate-secrets|setup-dynamic-secrets}" >&2
        exit 1
        ;;
esac
