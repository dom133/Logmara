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
#   setup-dynamic-secrets    Configure dynamic secrets engines (PG, RabbitMQ - not Redis,
#                            see the comment in the setup-dynamic-secrets case below)
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

# Read a secret's value back from Vault KV v2 (same path convention as
# vault_kv_write - display path, not the raw "data/"-prefixed API path).
# Prints nothing (not an error) if the secret doesn't exist, so callers
# should treat an empty result as "not set" the same way they'd treat an
# unset env var.
vault_kv_read() {
    local path="$1"
    local key="${2:-value}"
    vault_cli kv get -field="$key" "$path" 2>/dev/null || true
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
        # API path, unlike the `vault kv` CLI subcommands). create+update
        # (on top of read) let the api service's automatic secret rotation
        # (backend/vaultclient.StartRotation) write its own rotated
        # jwt_secret/encryption_key back to Vault every 24h.
        vault_cli policy write logmara - <<'EOF'
path "secret/data/logmara/*" {
  capabilities = ["read", "create", "update"]
}
EOF

        # Grants read access to the dynamic secrets engines that
        # `setup-dynamic-secrets` provisions. Written unconditionally here
        # (not only by setup-dynamic-secrets) so it already exists by the
        # time `migrate-secrets` creates the agent token below and attaches
        # it - Vault token policies are fixed at creation and can't be
        # added later, so the policy must exist first regardless of
        # whether the dynamic engines themselves are set up yet.
        vault_cli policy write logmara-dynamic - <<'EOF'
path "secret-dynamic/database/*" {
  capabilities = ["read"]
}
path "secret-dynamic/rabbitmq/*" {
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
        for SECRET in jwt_secret encryption_key token_hash_key maintenance_token jwt_private_key pg_app_password pg_superuser_password pg_replication_password redis_password rabbitmq_password; do
            VALUE=$(read_docker_secret_value "$SECRET")
            if [[ -n "$VALUE" ]]; then
                echo "Migrating $SECRET..."
                vault_kv_write "secret/logmara/$SECRET" "value" "$VALUE"
            fi
        done

        # Create the bootstrap token every direct-API consumer authenticates
        # Vault calls with, and distribute it as a Docker secret (Swarm hands
        # it to Patroni/Redis/RabbitMQ/api at /run/secrets/vault_agent_token
        # - see patroni/entrypoint.sh, redis/entrypoint.sh,
        # rabbitmq/entrypoint.sh, backend/vaultclient). It is NOT stored in
        # Vault KV: it's needed to authenticate to Vault in the first place,
        # so storing it as a Vault secret would be circular.
        echo "Creating bootstrap token..."
        AGENT_TOKEN=$(vault_cli token create \
            -policy=logmara \
            -policy=logmara-dynamic \
            -ttl=24h \
            -period=24h \
            -format=json 2>/dev/null | jq -r '.auth.client_token')

        if [[ -z "$AGENT_TOKEN" || "$AGENT_TOKEN" == "null" ]]; then
            echo "ERROR: Failed to create bootstrap token" >&2
            exit 1
        fi

        if docker secret inspect vault_agent_token &>/dev/null; then
            echo "Docker secret 'vault_agent_token' already exists - leaving it as-is."
            echo "(This token can't be rotated in place; to replace it, remove the"
            echo " secret and every service referencing it, then re-run this command.)"
        else
            printf '%s' "$AGENT_TOKEN" | docker secret create vault_agent_token -
            echo "Docker secret 'vault_agent_token' created."
        fi

        echo "=== Secrets migrated ==="
        echo "Now deploy postgres/redis/rabbitmq/app (each fetches its own secrets"
        echo "from Vault directly at startup, see README 'Deploying Vault'):"
        echo "  ./scripts/swarm-deploy.sh postgres"
        ;;

    setup-dynamic-secrets)
        echo "=== Setting up dynamic secrets engines ==="

        if [[ -z "${VAULT_TOKEN:-}" ]]; then
            echo "ERROR: VAULT_TOKEN not set" >&2
            exit 1
        fi

        # Both passwords below were already migrated into Vault KV by
        # `migrate-secrets` (pg_superuser_password, rabbitmq_password) - read
        # them from there by default instead of making the operator supply
        # them again. An explicit env var still wins if set, for the rare
        # case they differ from what's in Vault.
        PG_SUPERUSER_PASSWORD="${PG_SUPERUSER_PASSWORD:-$(vault_kv_read secret/logmara/pg_superuser_password)}"
        if [[ -z "$PG_SUPERUSER_PASSWORD" ]]; then
            echo "ERROR: pg_superuser_password not found in Vault, and PG_SUPERUSER_PASSWORD not set." >&2
            echo "       This is the Patroni/Postgres 'postgres' superuser role's password (not a" >&2
            echo "       separate 'vault' account - there isn't one). Run" >&2
            echo "       './scripts/vault-bootstrap.sh migrate-secrets' first, or set it explicitly:" >&2
            echo "         export PG_SUPERUSER_PASSWORD=\$(docker exec \$(docker ps -q --filter name=logmara-pg_postgres --filter status=running | head -1) cat /run/secrets/pg_superuser_password)" >&2
            exit 1
        fi

        # RabbitMQ has no separate admin account to reuse: rabbitmq.conf.tpl
        # creates exactly one user, "logmara" (see rabbitmq/rabbitmq.conf.tpl),
        # whose password is the same rabbitmq_password already in Vault.
        RABBITMQ_DEFAULT_USER="${RABBITMQ_DEFAULT_USER:-logmara}"
        RABBITMQ_DEFAULT_PASS="${RABBITMQ_DEFAULT_PASS:-$(vault_kv_read secret/logmara/rabbitmq_password)}"
        if [[ -z "$RABBITMQ_DEFAULT_PASS" ]]; then
            echo "ERROR: rabbitmq_password not found in Vault, and RABBITMQ_DEFAULT_PASS not set." >&2
            echo "       Run './scripts/vault-bootstrap.sh migrate-secrets' first, or set it explicitly." >&2
            exit 1
        fi

        # Enable PostgreSQL dynamic secrets engine
        echo "Enabling PostgreSQL dynamic secrets engine..."
        vault_cli secrets enable -path=secret-dynamic/database database 2>/dev/null || true
        vault_cli write secret-dynamic/database/config/db \
            plugin_name=postgresql-database-plugin \
            allowed_roles="logmara-app" \
            connection_url="postgresql://${PG_SUPERUSER:-postgres}:${PG_SUPERUSER_PASSWORD}@haproxy:5000/${PG_DB:-syslog_db}?sslmode=disable" \
            username="${PG_SUPERUSER:-postgres}" \
            password="${PG_SUPERUSER_PASSWORD}" 2>/dev/null || true

        # Create a persistent group role that holds all privileges.
        # Dynamic users are granted membership in this group instead of
        # per-table GRANTs, which avoids "tuple concurrently updated"
        # (SQLSTATE XX000) errors when Vault's CREATE ROLE races with
        # VACUUM / MV refresh / partition creation.
        #
        # This block runs unconditionally every time setup-dynamic-secrets
        # is called, so that tables/sequences added by a newer migration
        # also receive the grants and ownership transfer. Without it, a
        # migration that runs ALTER TABLE on a table owned by postgres
        # will fail with "must be owner of table <name>".
        docker run --rm \
          --network syslog_net \
          -e PGPASSWORD="$PG_SUPERUSER_PASSWORD" \
          postgres:16-alpine psql -h haproxy -p 5000 \
            -U "${PG_SUPERUSER:-postgres}" \
            -d "${PG_DB:-syslog_db}" \
            -c "
DO \$\$ BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'logmara_app_group') THEN
        CREATE ROLE logmara_app_group;
    END IF;
END \$\$;

GRANT CREATE ON SCHEMA public TO logmara_app_group;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO logmara_app_group;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO logmara_app_group;

DO \$\$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT tablename
        FROM pg_tables
        WHERE schemaname = 'public'
          AND tableowner != 'logmara_app_group'
    LOOP
        EXECUTE format('ALTER TABLE %I OWNER TO logmara_app_group', r.tablename);
    END LOOP;
END \$\$;

DO \$\$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT matviewname
        FROM pg_matviews
        WHERE schemaname = 'public'
          AND matviewowner != 'logmara_app_group'
    LOOP
        EXECUTE format('ALTER MATERIALIZED VIEW %I OWNER TO logmara_app_group', r.matviewname);
    END LOOP;
END \$\$;

ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT ALL PRIVILEGES ON TABLES TO logmara_app_group;

ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT ALL PRIVILEGES ON SEQUENCES TO logmara_app_group;

ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT ALL PRIVILEGES ON MATERIALIZED VIEWS TO logmara_app_group;"

        # SECURITY DEFINER function so any app user (even after Vault rotates
        # the dynamic credentials) can refresh the dashboard materialized
        # views. The function runs as postgres (the owner), so ownership of
        # the views themselves doesn't matter.
        docker run --rm \
          --network syslog_net \
          -e PGPASSWORD="$PG_SUPERUSER_PASSWORD" \
          postgres:16-alpine psql -h haproxy -p 5000 \
            -U "${PG_SUPERUSER:-postgres}" \
            -d "${PG_DB:-syslog_db}" \
            -c "CREATE OR REPLACE FUNCTION refresh_mv(name TEXT)
                RETURNS VOID AS \$\$
                BEGIN
                  EXECUTE format('REFRESH MATERIALIZED VIEW CONCURRENTLY %I', name);
                EXCEPTION WHEN object_not_in_prerequisite_state THEN
                  EXECUTE format('REFRESH MATERIALIZED VIEW %I', name);
                END;
                \$\$ LANGUAGE plpgsql SECURITY DEFINER;"

        docker run --rm \
          --network syslog_net \
          -e PGPASSWORD="$PG_SUPERUSER_PASSWORD" \
          postgres:16-alpine psql -h haproxy -p 5000 \
            -U "${PG_SUPERUSER:-postgres}" \
            -d "${PG_DB:-syslog_db}" \
            -c "GRANT EXECUTE ON FUNCTION refresh_mv(TEXT) TO logmara_app_group;"

        # Create role for application user
        vault_cli write secret-dynamic/database/roles/logmara-app \
            db_name=db \
            creation_statements="CREATE ROLE \"{{name}}\" WITH LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}'; GRANT logmara_app_group TO \"{{name}}\";" \
            default_ttl="48h" \
            max_ttl="72h" 2>/dev/null || true

        # Redis dynamic secrets are deliberately NOT set up here. Both the
        # official redis-database-plugin and the community
        # vault-plugin-secrets-redis only take a fixed host:port - neither
        # discovers the current master through Sentinel - so pointing one
        # at any single redis1/2/3 node silently goes stale on the next
        # Sentinel failover. It's also the wrong tool for this topology
        # even ignoring that: Sentinel-managed Redis shares one
        # requirepass/ACL across the whole replication set, not a
        # per-instance dynamic user.
        #
        # This also means backend/vaultclient.StartRotation's 24h loop
        # cannot auto-rotate the Redis password the way it does JWT/
        # encryption_key: those two are generated AND consumed entirely
        # inside the api process, so it's safe for api to mint a new value
        # unilaterally. The Redis password is enforced by redis1/2/3
        # themselves - api changing it unilaterally would just lock itself
        # (and everyone else) out, since nothing would have told the Redis
        # servers to accept the new value. Rotating it for real needs a
        # coordinated CONFIG SET/config-reload across all 3 Sentinel-
        # monitored nodes, which is out of scope here.
        #
        # Redis password rotation therefore stays manual: run
        # scripts/rotate-secrets.sh (it already coordinates the Redis-side
        # change with the secret/data/logmara/redis_password KV write),
        # then `docker service update --force logmara-app_api`.
        # sharedstate.Client.RotatePassword / the RotateRedisPassword
        # callback do exist and would hot-swap api's live connection
        # without that restart, but nothing currently triggers them
        # automatically - they're just no longer dead code once something
        # does.

        # Enable RabbitMQ dynamic secrets engine (built-in in Vault 1.16)
        echo "Enabling RabbitMQ dynamic secrets engine..."
        vault_cli secrets enable -path=secret-dynamic/rabbitmq rabbitmq 2>/dev/null || true
        vault_cli write secret-dynamic/rabbitmq/config/connection \
            connection_uri="http://haproxy-rabbitmq:15672" \
            username="${RABBITMQ_DEFAULT_USER}" \
            password="${RABBITMQ_DEFAULT_PASS}" 2>/dev/null || true
        vault_cli write secret-dynamic/rabbitmq/roles/logmara-app \
            vhosts='{"/":{"configure": ".*", "write": ".*", "read": ".*"}}' \
            tags="administrator,management" \
            default_ttl="48h" \
            max_ttl="72h" 2>/dev/null || true

        # Update policy to allow dynamic secret reads
        vault_cli policy write logmara-dynamic - <<'EOF'
path "secret-dynamic/database/*" {
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
