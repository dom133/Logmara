#!/usr/bin/env bash
#
# Rotate secrets, either with a zero-downtime rolling restart of the api
# service (`rotate`), or as a batch across several secrets with logmara-app
# taken fully offline for the duration (`rotate-batch`).
#
# Usage:
#   ./scripts/rotate-secrets.sh rotate <secret-name> <new-value>
#   ./scripts/rotate-secrets.sh rotate-batch <secret-name>=<new-value> [<secret-name>=<new-value> ...]
#   ./scripts/rotate-secrets.sh status
#   ./scripts/rotate-secrets.sh list
#
# `rotate` rolling-restarts just the affected services (including
# logmara-app_api), so the app stays up throughout - the standard path for
# rotating one secret.
#
# `rotate-batch` is for rotating several secrets together: it scales
# logmara-app_api and logmara-app_frontend to 0 *before* touching any
# secret, applies every <secret-name>=<new-value> pair, then scales both
# back to their previous replica counts. Use this when you want a clean
# maintenance window instead of several independent rolling restarts (e.g.
# a full credential rotation touching jwt_secret, encryption_key, and the
# database passwords at once). If any secret in the batch fails to apply,
# the batch aborts and logmara-app is scaled back up regardless.
#
# Supported secrets:
#   - redis_password
#   - rabbitmq_password
#   - pg_app_password
#   - pg_superuser_password
#   - jwt_secret
#   - encryption_key  (special: triggers DB re-encryption)
#
# For password-based secrets (rabbitmq, redis, postgres) the new password
# is applied inside the running service BEFORE the secret value is updated,
# so a brief window exists where both old and new password work.
#
# For encryption_key rotation:
#   1. Reads old key from current secret store
#   2. Decrypts all notification_channels.secret (non-NULL rows)
#   3. Decrypts app_settings.value for smtp_password and ldap_bind_password
#   4. Re-encrypts all with new key
#   5. Updates secret store
#   6. Rolling restarts all api services (or relies on rotate-batch's
#      scale-down/up if run as part of a batch)
#   7. Verifies services are healthy
#
# WARNING:
#   - jwt_secret rotation invalidates all user sessions
#   - encryption_key rotation requires DB access via haproxy:5000
#
# Not rotatable (intentionally excluded):
#   - pg_replication_password  (Patroni-managed, requires cluster re-init)
#   - rabbitmq_erlang_cookie  (requires full RabbitMQ cluster re-init)
#

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

ACTION="${1:-}"
SECRET_NAME="${2:-}"
NEW_VALUE="${3:-}"

if [[ -z "$ACTION" ]]; then
    echo "Usage: $0 rotate <secret-name> <new-value> | rotate-batch <secret-name>=<new-value> [...] | status | list" >&2
    exit 1
fi

# Set by scale_app_down; the EXIT trap uses it to guarantee logmara-app
# gets scaled back up even if a batch rotation fails or is interrupted.
APP_SCALED_DOWN=false

restore_app_scale_on_exit() {
    if [[ "$APP_SCALED_DOWN" == "true" ]]; then
        echo "=== (cleanup) restoring logmara-app scale ===" >&2
        scale_app_up
    fi
}
trap restore_app_scale_on_exit EXIT

# ---------------------------------------------------------------------------
# Secret store detection: Vault or Docker secrets
# ---------------------------------------------------------------------------

# Returns 0 if Vault agent is deployed and serving
using_vault() {
    docker service inspect logmara-vault_vault-agent &>/dev/null 2>&1 && \
    docker service ps --filter "desired-state=running" --filter "current-state=Running" logmara-vault_vault-agent 2>/dev/null | grep -q .
}

# Read the plaintext value of an existing Docker (Swarm) secret. `docker
# run --secret` is not a real flag - secrets are only mountable by Swarm
# services - so this spins up a one-shot, non-restarting service to read
# it via `docker service logs` and then removes it.
read_docker_secret_value() {
    local name="$1"
    local svc="rotate-secrets-read-$$-$RANDOM"
    docker service create --quiet --name "$svc" \
        --secret "$name" \
        --restart-condition none \
        --network syslog_net \
        alpine cat "/run/secrets/$name" >/dev/null 2>&1 || true
    local value=""
    for _ in $(seq 1 15); do
        value="$(docker service logs "$svc" 2>/dev/null | tr -d '\r\n')"
        [[ -n "$value" ]] && break
        sleep 1
    done
    docker service rm "$svc" >/dev/null 2>&1 || true
    printf '%s' "$value"
}

# Read a secret value from the active store. NOTE: `vault kv get/put`
# already prepends "data/" internally for a kv-v2 mount, so the path here
# must be the display path (e.g. "secret/logmara/foo"), not the raw API
# path "secret/data/logmara/foo" - the latter double-prepends and misses.
read_secret() {
    local name="$1"
    if using_vault; then
        local vault_token
        vault_token=$(cat /srv/syslog-ha/vault-token 2>/dev/null || echo "")
        docker run --rm \
            --network syslog_net \
            -e VAULT_ADDR="http://vault-1:8200" \
            -e VAULT_TOKEN="$vault_token" \
            hashicorp/vault:1.15.0 kv get -field=value "secret/logmara/$name" 2>/dev/null || echo ""
    else
        read_docker_secret_value "$name"
    fi
}

# Write a new secret value to the active store. Docker secrets are
# immutable - `docker secret create <name>` fails once <name> already
# exists (which it always will after the first deploy) - so the Docker
# secrets branch creates a new versioned secret and repoints every service
# currently referencing the old name at it (rolling update), keeping the
# same in-container target path so the app's *_FILE env vars don't change.
write_secret() {
    local name="$1"
    local value="$2"
    if using_vault; then
        local vault_token
        vault_token=$(cat /srv/syslog-ha/vault-token 2>/dev/null || echo "")
        local tmpfile
        tmpfile=$(mktemp)
        printf '%s' "$value" > "$tmpfile"
        docker run --rm \
            --network syslog_net \
            -e VAULT_ADDR="http://vault-1:8200" \
            -e VAULT_TOKEN="$vault_token" \
            -v "$tmpfile:/tmp/secretval:ro" \
            hashicorp/vault:1.15.0 sh -c "
                VAL=\$(cat /tmp/secretval) && \
                vault kv put \"secret/logmara/$name\" \"value=\$VAL\"
            " 2>/dev/null || true
        rm -f "$tmpfile"
    else
        local versioned_name="${name}_$(date +%s)"
        echo "$value" | docker secret create "$versioned_name" -

        local svc
        for svc in $(docker service ls --format '{{.Name}}'); do
            if docker service inspect "$svc" \
                --format '{{range .Spec.TaskTemplate.ContainerSpec.Secrets}}{{.SecretName}} {{end}}' 2>/dev/null \
                | grep -qw "$name"; then
                echo "  Repointing $svc: $name -> $versioned_name"
                docker service update --quiet \
                    --secret-rm "$name" \
                    --secret-add "source=$versioned_name,target=$name" \
                    "$svc"
            fi
        done
    fi
}

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

wait_for_healthy() {
    local service="$1"
    local timeout="${2:-120}"
    local elapsed=0
    echo "Waiting for $service to become healthy..."
    while [[ $elapsed -lt $timeout ]]; do
        local state
        state=$(docker service ps --format '{{.CurrentState}}' --filter "desired-state=running" "$service" 2>/dev/null | head -1)
        if [[ "$state" == *"Running"* ]]; then
            echo "$service is healthy after ${elapsed}s"
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done
    echo "ERROR: $service did not become healthy within ${timeout}s" >&2
    return 1
}

rolling_restart() {
    local services=("$@")
    for svc in "${services[@]}"; do
        if docker service inspect "$svc" &>/dev/null; then
            echo "Restarting $svc..."
            docker service update --force "$svc"
            wait_for_healthy "$svc" 120
        fi
    done
}

# ---------------------------------------------------------------------------
# logmara-app scale down/up, used by rotate-batch to take the app fully
# offline for the duration of a multi-secret rotation.
# ---------------------------------------------------------------------------

API_REPLICAS_SAVED=""
FRONTEND_REPLICAS_SAVED=""

wait_for_scale_zero() {
    local service="$1"
    local timeout="${2:-60}"
    local elapsed=0
    echo "Waiting for $service to scale down to 0..."
    while [[ $elapsed -lt $timeout ]]; do
        local running
        running=$(docker service ps --filter "desired-state=running" --format '{{.ID}}' "$service" 2>/dev/null | wc -l)
        if [[ "$running" -eq 0 ]]; then
            echo "$service is at 0 running tasks"
            return 0
        fi
        sleep 2
        elapsed=$((elapsed + 2))
    done
    echo "WARNING: $service still has running tasks after ${timeout}s" >&2
}

scale_app_down() {
    echo "=== Scaling logmara-app (api, frontend) down to 0 ==="
    API_REPLICAS_SAVED=$(docker service inspect logmara-app_api --format '{{.Spec.Mode.Replicated.Replicas}}' 2>/dev/null || echo "")
    FRONTEND_REPLICAS_SAVED=$(docker service inspect logmara-app_frontend --format '{{.Spec.Mode.Replicated.Replicas}}' 2>/dev/null || echo "")
    if [[ -z "$API_REPLICAS_SAVED" || -z "$FRONTEND_REPLICAS_SAVED" ]]; then
        echo "ERROR: could not read current replica counts for logmara-app_api/logmara-app_frontend - is logmara-app deployed?" >&2
        exit 1
    fi
    echo "Current replicas: api=$API_REPLICAS_SAVED frontend=$FRONTEND_REPLICAS_SAVED"

    docker service scale "logmara-app_api=0" "logmara-app_frontend=0"
    APP_SCALED_DOWN=true
    wait_for_scale_zero logmara-app_api
    wait_for_scale_zero logmara-app_frontend
}

scale_app_up() {
    echo "=== Scaling logmara-app back up (api=$API_REPLICAS_SAVED, frontend=$FRONTEND_REPLICAS_SAVED) ==="
    docker service scale "logmara-app_api=$API_REPLICAS_SAVED" "logmara-app_frontend=$FRONTEND_REPLICAS_SAVED"
    APP_SCALED_DOWN=false
    wait_for_healthy logmara-app_api 120
    wait_for_healthy logmara-app_frontend 120
}

# ---------------------------------------------------------------------------
# Password change logic per service
# ---------------------------------------------------------------------------

# Change RabbitMQ user password inside the running service
change_rabbitmq_password() {
    local new_pass="$1"
    echo "Changing RabbitMQ 'logmara' user password..."
    # Use the first running RabbitMQ node
    local container_id
    container_id=$(docker ps -q --filter "name=logmara-rabbitmq" --filter "status=running" | head -1)
    if [[ -z "$container_id" ]]; then
        echo "ERROR: No running RabbitMQ container found" >&2
        return 1
    fi
    docker exec "$container_id" rabbitmqctl change_password logmara "$new_pass"
    echo "RabbitMQ password changed successfully"
}

# Change Redis password inside the running service (all 3 nodes)
change_redis_password() {
    local new_pass="$1"
    echo "Changing Redis password..."
    local old_pass
    old_pass=$(read_secret redis_password)
    # Apply to all running Redis nodes
    local container_ids
    container_ids=$(docker ps -q --filter "name=logmara-redis" --filter "status=running")
    if [[ -z "$container_ids" ]]; then
        echo "ERROR: No running Redis container found" >&2
        return 1
    fi
    for cid in $container_ids; do
        echo "  Updating Redis node $cid..."
        docker exec "$cid" redis-cli -a "$old_pass" CONFIG SET requirepass "$new_pass" 2>/dev/null || true
        docker exec "$cid" redis-cli -a "$old_pass" CONFIG SET masterauth "$new_pass" 2>/dev/null || true
    done
    echo "Redis password changed successfully"
}

# Change Postgres user password inside the running service
change_postgres_password() {
    local username="$1"
    local new_pass="$2"
    echo "Changing Postgres '$username' user password..."
    local old_root_pass
    old_root_pass=$(read_secret pg_superuser_password)
    # Use the first running Postgres node
    local container_id
    container_id=$(docker ps -q --filter "name=logmara-pg" --filter "status=running" | head -1)
    if [[ -z "$container_id" ]]; then
        echo "ERROR: No running Postgres container found" >&2
        return 1
    fi
    # Pass PGPASSWORD for auth, connect via localhost to use md5/scram auth
    docker exec -e PGPASSWORD="$old_root_pass" "$container_id" \
        psql -h localhost -U postgres -c \
        "ALTER USER $username WITH PASSWORD '$(echo "$new_pass" | sed "s/'/''/g")';"
    echo "Postgres password for '$username' changed successfully"
}

# ---------------------------------------------------------------------------
# Rotates a single secret: live password change, optional encryption_key
# re-encryption, store update, warnings, and (unless skip_app_restart) a
# rolling restart of logmara-app_api. Returns non-zero if the rotation
# didn't complete (declined confirmation or a failed step) - callers must
# check this rather than relying on `set -e`, since rotate-batch needs to
# abort the whole batch (and still scale logmara-app back up) rather than
# die mid-loop.
# ---------------------------------------------------------------------------
rotate_one() {
    local name="$1"
    local value="$2"
    local skip_app_restart="${3:-false}"

    echo "=== Rotating secret: $name ==="

    # Step 1: apply password change inside the running service (if applicable)
    case "$name" in
        rabbitmq_password) change_rabbitmq_password "$value" ;;
        redis_password) change_redis_password "$value" ;;
        pg_app_password) change_postgres_password "syslog" "$value" ;;
        pg_superuser_password) change_postgres_password "postgres" "$value" ;;
    esac

    # Step 2: special handling for encryption_key (DB re-encryption)
    if [[ "$name" == "encryption_key" ]]; then
        echo "WARNING: Rotating encryption_key will re-encrypt sensitive data in the database."
        echo "This may take a moment depending on the amount of encrypted data."
        read -p "Continue? (y/N): " confirm
        if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
            echo "Aborted rotating $name."
            return 1
        fi

        local old_key old_pg_app_pass
        old_key=$(read_secret encryption_key)
        if [[ -z "$old_key" ]]; then
            echo "ERROR: Could not read old encryption key" >&2
            return 1
        fi
        old_pg_app_pass=$(read_secret pg_app_password)

        echo "Re-encrypting database with new key..."
        docker run --rm \
            -e OLD_ENCRYPTION_KEY="$old_key" \
            -e NEW_ENCRYPTION_KEY="$value" \
            -e PG_HOST=haproxy \
            -e PG_PORT=5000 \
            -e PG_USER=syslog \
            -e PG_PASSWORD="$old_pg_app_pass" \
            -e PG_DB=syslog_db \
            -e PG_SSLMODE=disable \
            -v "$REPO_ROOT:/app" \
            golang:1.21-alpine \
            sh -c 'cd /app && go run backend/cmd/rotatekey/main.go' || {
                echo "ERROR: Database re-encryption failed" >&2
                return 1
            }
        echo "Database re-encryption complete."
    fi

    # Step 3: update the secret in the store
    write_secret "$name" "$value"
    echo "Secret '$name' updated in store"

    # Step 4: warnings
    if [[ "$name" == "jwt_secret" ]]; then
        echo "WARNING: All user sessions will be invalidated!"
    fi

    # Step 5: rolling restart of affected services. logmara-app_api/frontend
    # are excluded when skip_app_restart=true - rotate-batch's scale-down/up
    # already takes care of them once, after every secret in the batch has
    # been applied, instead of restarting api once per secret.
    if [[ "$skip_app_restart" == "true" ]]; then
        case "$name" in
            redis_password)
                rolling_restart "logmara-redis_redis1" "logmara-redis_redis2" "logmara-redis_redis3"
                ;;
            rabbitmq_password)
                rolling_restart "logmara-rabbitmq_rabbitmq1" "logmara-rabbitmq_rabbitmq2" "logmara-rabbitmq_rabbitmq3"
                ;;
            pg_app_password|pg_superuser_password)
                rolling_restart "logmara-pg_postgres1" "logmara-pg_postgres2" "logmara-pg_postgres3"
                ;;
        esac
    else
        case "$name" in
            redis_password)
                rolling_restart \
                    "logmara-redis_redis1" \
                    "logmara-redis_redis2" \
                    "logmara-redis_redis3" \
                    "logmara-app_api"
                ;;
            rabbitmq_password)
                rolling_restart \
                    "logmara-rabbitmq_rabbitmq1" \
                    "logmara-rabbitmq_rabbitmq2" \
                    "logmara-rabbitmq_rabbitmq3" \
                    "logmara-app_api"
                ;;
            pg_app_password|pg_superuser_password)
                rolling_restart \
                    "logmara-pg_postgres1" \
                    "logmara-pg_postgres2" \
                    "logmara-pg_postgres3" \
                    "logmara-app_api"
                ;;
            *)
                rolling_restart "logmara-app_api"
                ;;
        esac
    fi

    echo "=== Secret rotation complete: $name ==="
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

case "$ACTION" in
    list)
        if using_vault; then
            echo "=== Vault Secrets (KV v2) ==="
            docker run --rm \
                --network syslog_net \
                -e VAULT_ADDR="http://vault-1:8200" \
                -e VAULT_TOKEN="$(cat /srv/syslog-ha/vault-token 2>/dev/null || echo '')" \
                hashicorp/vault:1.15.0 kv list secret/logmara/ 2>/dev/null || echo "(unable to list)"
        else
            echo "=== Docker Secrets ==="
            docker secret ls
        fi
        ;;

    status)
        if using_vault; then
            echo "=== Secret Store: HashiCorp Vault ==="
            docker service ls --filter "name=logmara-vault" 2>/dev/null || echo "(vault not deployed)"
        else
            echo "=== Secret Store: Docker Secrets ==="
            docker secret ls --format 'table {{.Name}}\t{{.ID}}\t{{.CreatedAt}}'
        fi
        ;;

    rotate)
        if [[ -z "$SECRET_NAME" || -z "$NEW_VALUE" ]]; then
            echo "Usage: $0 rotate <secret-name> <new-value>" >&2
            exit 1
        fi

        echo "Secret store: $(if using_vault; then echo 'HashiCorp Vault'; else echo 'Docker Secrets'; fi)"

        rotate_one "$SECRET_NAME" "$NEW_VALUE" false
        ;;

    rotate-batch)
        shift || true   # drop "rotate-batch", leaving the <name>=<value> pairs in "$@"
        if [[ $# -eq 0 ]]; then
            echo "Usage: $0 rotate-batch <secret-name>=<new-value> [<secret-name>=<new-value> ...]" >&2
            exit 1
        fi

        declare -A BATCH_VALUES=()
        BATCH_ORDER=()
        for pair in "$@"; do
            if [[ "$pair" != *=* ]]; then
                echo "ERROR: invalid argument '$pair', expected <secret-name>=<new-value>" >&2
                exit 1
            fi
            name="${pair%%=*}"
            value="${pair#*=}"
            if [[ -z "$value" ]]; then
                echo "ERROR: empty value for '$name'" >&2
                exit 1
            fi
            BATCH_VALUES["$name"]="$value"
            BATCH_ORDER+=("$name")
        done

        echo "=== Batch rotation: ${BATCH_ORDER[*]} ==="
        echo "Secret store: $(if using_vault; then echo 'HashiCorp Vault'; else echo 'Docker Secrets'; fi)"

        scale_app_down

        for name in "${BATCH_ORDER[@]}"; do
            if ! rotate_one "$name" "${BATCH_VALUES[$name]}" true; then
                echo "ERROR: rotating '$name' failed - aborting batch." >&2
                exit 1
            fi
        done

        scale_app_up

        echo "=== Batch rotation complete: ${BATCH_ORDER[*]} ==="
        ;;

    *)
        echo "Unknown action: $ACTION" >&2
        exit 1
        ;;
esac
