#!/bin/sh
set -e

# Accept passwords either as plain env vars or, preferably, via Docker/Swarm
# secrets mounted under /run/secrets — falls back to the plain var if no
# secret file is present so this also works with `docker compose` for local
# testing of this image.
: "${PATRONI_SUPERUSER_PASSWORD:=$(cat /run/secrets/pg_superuser_password 2>/dev/null || true)}"
: "${PATRONI_REPLICATION_PASSWORD:=$(cat /run/secrets/pg_replication_password 2>/dev/null || true)}"
: "${POSTGRES_PASSWORD:=$(cat /run/secrets/pg_app_password 2>/dev/null || true)}"

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
