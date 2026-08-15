#!/bin/sh
set -e

# Fetch secrets from Vault API. vault_agent_token is still a Swarm secret
# mounted at /run/secrets/vault_agent_token — it's only used for auth, not
# stored on disk alongside the actual passwords.
# Falls back to plain env vars if VAULT_ADDR is unset (local docker-compose).

vault_fetch() {
    local name="$1"
    local response
    response=$(curl -sf \
        -H "X-Vault-Token: $(cat /run/secrets/vault_agent_token)" \
        "${VAULT_ADDR}/v1/secret/data/logmara/${name}" 2>/dev/null) || true
    if [ -n "$response" ]; then
        echo "$response" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['data']['value'])" 2>/dev/null || true
    fi
}

: "${PATRONI_SUPERUSER_PASSWORD:=$(vault_fetch pg_superuser_password)}"
: "${PATRONI_REPLICATION_PASSWORD:=$(vault_fetch pg_replication_password)}"
: "${POSTGRES_PASSWORD:=$(vault_fetch pg_app_password)}"

if [ -z "$PATRONI_NAME" ]; then
    echo "PATRONI_NAME must be set (unique per node, e.g. postgres1)" >&2
    exit 1
fi
if [ -z "$PATRONI_SUPERUSER_PASSWORD" ] || [ -z "$PATRONI_REPLICATION_PASSWORD" ] || [ -z "$POSTGRES_PASSWORD" ]; then
    echo "PATRONI_SUPERUSER_PASSWORD, PATRONI_REPLICATION_PASSWORD and POSTGRES_PASSWORD must all be set" >&2
    exit 1
fi

export PATRONI_SUPERUSER_PASSWORD PATRONI_REPLICATION_PASSWORD POSTGRES_PASSWORD
export POSTGRES_USER="${POSTGRES_USER:-syslog}"
export POSTGRES_DB="${POSTGRES_DB:-syslog_db}"

mkdir -p /home/postgres/pgdata
chown -R postgres:postgres /home/postgres
chmod 700 /home/postgres/pgdata

envsubst < /patroni.yml.tpl > /patroni.yml

exec su-exec postgres patroni /patroni.yml
