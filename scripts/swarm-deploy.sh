#!/usr/bin/env bash
#
# Deploys one or more Docker Swarm stacks, sourcing variables from .env first.
#
# `docker stack deploy` does NOT read .env files — it only sees variables
# already exported in the shell. This wrapper bridges the gap by loading .env
# before invoking stack deploy, so you get the same workflow as docker compose.
#
# Usage:
#   ./scripts/swarm-deploy.sh <stack> [stack-name]
#
#   <stack>       One of: vault, postgres, redis, rabbitmq, app, monitoring, all
#   [stack-name]  Override the stack name (default: logmara-vault /
#                 logmara-pg / logmara-redis / logmara-rabbitmq / logmara-app / logmara-monitoring)
#
# Examples:
#   ./scripts/swarm-deploy.sh vault
#   ./scripts/swarm-deploy.sh postgres
#   ./scripts/swarm-deploy.sh rabbitmq
#   ./scripts/swarm-deploy.sh app
#   ./scripts/swarm-deploy.sh monitoring
#   ./scripts/swarm-deploy.sh all
#
# Note: `all` deploys postgres/redis/rabbitmq/app only, not vault/monitoring -
# those need one-time setup first (Vault's bootstrap in README "Deploying
# Vault"; monitoring's GRAFANA_ADMIN_PASSWORD and NFS dashboards path in
# README "Deploying Monitoring") and aren't part of the routine redeploy
# cycle the way the other four are.
#
# .env file location (first match wins):
#   1. .env in the same directory as this script's parent (repo root)
#   2. .env passed via --env-file /path/to/.env
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# ---------------------------------------------------------------------------
# Parse flags
# ---------------------------------------------------------------------------
ENV_FILE=""
RESOLVE_IMAGE=""
WITH_REGISTRY_AUTH=""

while [[ "${1:-}" == --* ]]; do
    case "$1" in
        --env-file)
            ENV_FILE="${2:?--env-file requires a path}"
            shift 2
            ;;
        --resolve-image)
            RESOLVE_IMAGE="--resolve-image always"
            shift
            ;;
        --with-registry-auth)
            WITH_REGISTRY_AUTH="--with-registry-auth"
            shift
            ;;
        *)
            echo "Unknown flag: $1" >&2
            exit 1
            ;;
    esac
done

STACK="${1:-}"
STACK_NAME_OVERRIDE="${2:-}"

if [[ -z "$STACK" ]]; then
    echo "Usage: $0 [--env-file <path>] [--resolve-image] [--with-registry-auth] <stack> [stack-name]" >&2
    echo "" >&2
     echo "Stacks: vault, postgres, redis, rabbitmq, app, monitoring, all" >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# Load .env
# ---------------------------------------------------------------------------
if [[ -n "$ENV_FILE" ]]; then
    if [[ ! -f "$ENV_FILE" ]]; then
        echo "Error: .env file not found: $ENV_FILE" >&2
        exit 1
    fi
    set -a
    # shellcheck disable=SC1091
    source "$ENV_FILE"
    set +a
    echo "Loaded .env from: $ENV_FILE"
elif [[ -f "$REPO_ROOT/.env" ]]; then
    set -a
    # shellcheck disable=SC1091
    source "$REPO_ROOT/.env"
    set +a
    echo "Loaded .env from: $REPO_ROOT/.env"
else
    echo "Warning: no .env file found (tried $REPO_ROOT/.env). Variables must be exported manually." >&2
fi

# Export every loaded var so docker stack deploy can see them
export REGISTRY TAG NFS_SERVER NFS_LOG_DATA_PATH NFS_LOG_SPOOL_PATH NFS_PARSER_DEFS_PATH
export POSTGRES_USER POSTGRES_DB POSTGRES_PASSWORD
export HTTPS_ENABLED HTTPS_REDIRECT CORS_ORIGINS RELAY_CENTRAL_HOST
export API_REPLICAS FRONTEND_REPLICAS
export FRONTEND_PORT FRONTEND_HTTPS_PORT HAPROXY_APP_STATS_PORT
export REDIS_PASSWORD
export IMAGE_TAG
export GRAFANA_ADMIN_USER GRAFANA_ADMIN_PASSWORD NFS_GRAFANA_DASHBOARDS_PATH
export MONITORING_PROMETHEUS_PORT MONITORING_ALERTMANAGER_PORT MONITORING_GRAFANA_PORT

# ---------------------------------------------------------------------------
# Content-hash names for Swarm configs that aren't `external: true`
#
# `docker stack deploy` tries to update an existing (non-external) config
# object in place when its file content changes, and Swarm always rejects
# that ("only updates to Labels are allowed") - configs are immutable at
# the API level regardless of the external: flag. Each config below is
# instead named with a hash of its own file's content
# (docker-stack.*.yml's `name: foo_${FOO_HASH}`), so a changed file gets a
# brand new object name and the referencing service just does a normal
# rolling update - not a failed in-place update.
# ---------------------------------------------------------------------------
cfg_hash() {
    sha256sum "$REPO_ROOT/$1" | cut -c1-8
}

export PROMETHEUS_CFG_HASH="$(cfg_hash monitoring/prometheus.yml)"
export ALERTMANAGER_CFG_HASH="$(cfg_hash monitoring/alertmanager.yml)"
export ALERT_RULES_CFG_HASH="$(cfg_hash monitoring/alert_rules.yml)"
export GRAFANA_DATASOURCES_HASH="$(cfg_hash monitoring/grafana-datasources.yml)"
export GRAFANA_DASHBOARDS_CFG_HASH="$(cfg_hash monitoring/grafana-dashboards.yml)"
export RABBITMQ_CONF_TPL_HASH="$(cfg_hash rabbitmq/rabbitmq.conf.tpl)"
export RABBITMQ_ENTRYPOINT_HASH="$(cfg_hash rabbitmq/entrypoint.sh)"
export RABBITMQ_JOIN_ENTRYPOINT_HASH="$(cfg_hash rabbitmq/join_entrypoint.sh)"
export VAULT_CFG_HASH="$(cfg_hash vault/vault.hcl)"
export HAPROXY_PG_CFG_HASH="$(cfg_hash haproxy/haproxy.cfg)"
export HAPROXY_APP_CFG_HASH="$(cfg_hash haproxy/haproxy-app.cfg)"
export HAPROXY_RABBITMQ_CFG_HASH="$(cfg_hash haproxy/haproxy-rabbitmq.cfg)"
export REDIS_SENTINEL_CFG_HASH="$(cfg_hash redis/sentinel.conf.tpl)"
export REDIS_ENTRYPOINT_HASH="$(cfg_hash redis/entrypoint.sh)"
export REDIS_SENTINEL_ENTRYPOINT_HASH="$(cfg_hash redis/sentinel_entrypoint.sh)"

# ---------------------------------------------------------------------------
# Deploy helpers
# ---------------------------------------------------------------------------
deploy_stack() {
    local yaml="$1" name="$2"
    echo
    echo "=== Deploying $name ($yaml) ==="
    docker stack deploy $RESOLVE_IMAGE $WITH_REGISTRY_AUTH \
      -c "$REPO_ROOT/$yaml" "$name"
}

# ---------------------------------------------------------------------------
# Pre-update maintenance: pause ingest, compact logs, reset position
# ---------------------------------------------------------------------------
run_pre_update() {
    # Use a throwaway container on syslog_net to reach the api service
    # via Docker DNS. This works from any node, regardless of where
    # api tasks are placed, and does not depend on docker ps output.
    local helper="curlimages/curl"

    echo
    echo "=== Running pre-update maintenance ==="

    # Trigger pre-update preparation
    local http_code
    http_code=$(docker run --rm --network syslog_net --pull=missing "$helper" \
      -sf -o /dev/null -w "%{http_code}" \
      -X POST "http://api:8080/api/maintenance/pre-update" \
      --max-time 10 2>/dev/null) || true

    if [[ -z "$http_code" ]]; then
        echo "Warning: API is unreachable, cannot trigger pre-update"
        read -r -p "Continue deployment anyway? [y/N] " ans
        if [[ "$ans" != "y" ]] && [[ "$ans" != "Y" ]]; then
            echo "Aborted."
            exit 1
        fi
        return 0
    fi

    if [[ "$http_code" == "202" ]]; then
        echo "Pre-update trigger sent, waiting for completion..."
    elif [[ "$http_code" == "409" ]]; then
        echo "Pre-update already in progress, waiting for completion..."
    else
        echo "Warning: pre-update trigger failed (HTTP $http_code), proceeding with deploy"
        return 0
    fi

    # Poll for completion using a single container with an internal loop.
    # This avoids spawning 120+ containers (one per poll iteration).
    # --entrypoint sh overrides curlimages/curl's ENTRYPOINT ["curl"].
    local poll_result
    poll_result=$(docker run --rm --network syslog_net --pull=missing \
      --entrypoint sh "$helper" \
      -c '
        for i in $(seq 1 120); do
          status=$(curl -sf http://api:8080/api/maintenance/status --max-time 5 2>/dev/null) || true
          if echo "$status" | grep -q "completed"; then
            echo "completed"
            exit 0
          fi
          if (( i % 10 == 0 )); then
            echo "polling... ($i s)"
          fi
          sleep 1
        done
        echo "timeout"
        exit 1
      ' 2>/dev/null) || true

    if echo "$poll_result" | grep -q "completed"; then
        echo "Pre-update maintenance completed"
        return 0
    fi

    echo "Warning: pre-update timed out after 120s, proceeding with deploy anyway"
    return 0
}

# ---------------------------------------------------------------------------
# Deploy
# ---------------------------------------------------------------------------
case "$STACK" in
    vault)
        name="${STACK_NAME_OVERRIDE:-logmara-vault}"
        deploy_stack "docker-stack.vault.yml" "$name"
        ;;
    postgres)
        name="${STACK_NAME_OVERRIDE:-logmara-pg}"
        deploy_stack "docker-stack.postgres.yml" "$name"
        ;;
    redis)
        name="${STACK_NAME_OVERRIDE:-logmara-redis}"
        deploy_stack "docker-stack.redis.yml" "$name"
        ;;
    rabbitmq)
        name="${STACK_NAME_OVERRIDE:-logmara-rabbitmq}"
        deploy_stack "docker-stack.rabbitmq.yml" "$name"
        ;;
    app)
        name="${STACK_NAME_OVERRIDE:-logmara-app}"
        run_pre_update
        deploy_stack "docker-stack.app.yml" "$name"
        ;;
    monitoring)
        name="${STACK_NAME_OVERRIDE:-logmara-monitoring}"
        deploy_stack "docker-stack.monitoring.yml" "$name"
        ;;
    all)
        # Deploy in order: postgres -> redis -> rabbitmq -> app
        deploy_stack "docker-stack.postgres.yml" "${STACK_NAME_OVERRIDE:-logmara-pg}"
        deploy_stack "docker-stack.redis.yml"  "${STACK_NAME_OVERRIDE:-logmara-redis}"
        deploy_stack "docker-stack.rabbitmq.yml" "${STACK_NAME_OVERRIDE:-logmara-rabbitmq}"
        run_pre_update
        deploy_stack "docker-stack.app.yml"    "${STACK_NAME_OVERRIDE:-logmara-app}"
        ;;
    *)
        echo "Error: unknown stack '$STACK'. Use: vault, postgres, redis, rabbitmq, app, monitoring, all" >&2
        exit 1
        ;;
esac

echo
echo "Done. Run 'watch docker service ls' to track convergence."
