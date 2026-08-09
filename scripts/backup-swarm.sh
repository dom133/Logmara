#!/usr/bin/env bash
#
# Full backup of Docker Swarm state: etcd snapshot, Postgres dump, configs,
# secrets metadata, stack YAMLs, and join tokens.
#
# Run this on a Postgres node (pg1/pg2/pg3, i.e. wherever you'd normally
# `docker stack deploy` from) - the etcd snapshot and Postgres dump steps
# `docker exec` into whichever etcd/postgres container is running locally
# on that node (those images have etcdctl/pg_dump; the bare host doesn't,
# and /run/secrets/* only exists inside containers, not on the host). One
# etcd node's snapshot already has the full replicated cluster state, and
# pg_dump always targets haproxy:5000 (the current Postgres leader)
# regardless of which local postgres container runs it - so it doesn't
# matter which of pg1/pg2/pg3 you run this from, as long as it's one of them.
#
# Usage:
#   ./scripts/backup-swarm.sh [-d DIR] [--no-s3]
#
#   -d DIR       Backup directory (default: /srv/syslog-ha/backups)
#   --no-s3      Skip S3 sync even if BACKUP_S3_BUCKET is set
#
# The backup is timestamped and stored under $DIR/YYYY-MM-DD_HHMMSS/.
#

set -euo pipefail

BACKUP_DIR="/srv/syslog-ha/backups"
SYNC_S3=1

while [[ $# -gt 0 ]]; do
    case "$1" in
        -d) BACKUP_DIR="$2"; shift 2 ;;
        --no-s3) SYNC_S3=0; shift ;;
        *) echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

TIMESTAMP="$(date +%Y-%m-%d_%H%M%S)"
BACKUP_PATH="$BACKUP_DIR/$TIMESTAMP"

echo "=== Creating backup at $BACKUP_PATH ==="
mkdir -p "$BACKUP_PATH"

# 1. etcd snapshot - run inside a locally-running etcd container (has
#    etcdctl; the host doesn't). Any cluster member's snapshot has the full
#    replicated state, so whichever of etcd1/2/3 happens to be local is fine.
echo "[1/6] etcd snapshot..."
ETCD_CONTAINER="$(docker ps -q --filter "name=logmara-pg_etcd" --filter "status=running" | head -1)"
if [[ -n "$ETCD_CONTAINER" ]]; then
    if docker exec -e ETCDCTL_API=3 "$ETCD_CONTAINER" \
        etcdctl --endpoints=http://localhost:2379 snapshot save /tmp/etcd.snapshot 2>/dev/null \
        && docker cp "$ETCD_CONTAINER:/tmp/etcd.snapshot" "$BACKUP_PATH/etcd.snapshot" 2>/dev/null; then
        docker exec "$ETCD_CONTAINER" rm -f /tmp/etcd.snapshot 2>/dev/null || true
    else
        echo "WARNING: etcd snapshot failed" >&2
    fi
else
    echo "WARNING: no running etcd container found on this node, skipping etcd snapshot" >&2
fi

# 2. Postgres dump - run inside a locally-running postgres container (has
#    pg_dump and /run/secrets/pg_superuser_password; the host has neither).
#    Connects to haproxy:5000, which always routes to the current Patroni
#    leader regardless of which local postgres1/2/3 container runs this.
echo "[2/6] Postgres dump..."
PG_CONTAINER="$(docker ps -q --filter "name=logmara-pg_postgres" --filter "status=running" | head -1)"
if [[ -n "$PG_CONTAINER" ]]; then
    PG_SU_PASS="$(docker exec "$PG_CONTAINER" cat /run/secrets/pg_superuser_password 2>/dev/null || echo '')"
    if docker exec -e PGPASSWORD="$PG_SU_PASS" "$PG_CONTAINER" \
        pg_dump -h haproxy -p 5000 -U postgres -d syslog_db --format=custom --compress=9 \
            -f /tmp/postgres.dump 2>/dev/null \
        && docker cp "$PG_CONTAINER:/tmp/postgres.dump" "$BACKUP_PATH/postgres.dump" 2>/dev/null; then
        docker exec "$PG_CONTAINER" rm -f /tmp/postgres.dump 2>/dev/null || true
    else
        echo "WARNING: pg_dump failed" >&2
    fi
else
    echo "WARNING: no running postgres container found on this node, skipping Postgres dump" >&2
fi

# 3. Docker configs
echo "[3/6] Docker configs..."
docker config ls --format '{{.Name}}' | while read -r cfg_name; do
    docker config inspect "$cfg_name" --format '{{.ID}}' > "$BACKUP_PATH/config_${cfg_name}.id" 2>/dev/null || true
done

# 4. Docker secrets metadata (not values - values are protected by Docker)
echo "[4/6] Docker secrets metadata..."
docker secret ls --format '{{.Name}} {{.ID}} {{.CreatedAt}}' > "$BACKUP_PATH/secrets.list" 2>/dev/null || true

# 5. Stack YAMLs
echo "[5/6] Stack YAMLs..."
STACK_DIR="$BACKUP_PATH/stacks"
mkdir -p "$STACK_DIR"
for stack_file in docker-stack.*.yml; do
    if [[ -f "$stack_file" ]]; then
        cp "$stack_file" "$STACK_DIR/" 2>/dev/null || true
    fi
done

# 6. Swarm join tokens
echo "[6/6] Swarm join tokens..."
docker swarm join-token manager -q > "$BACKUP_PATH/join_token_manager.txt" 2>/dev/null || true
docker swarm join-token worker -q > "$BACKUP_PATH/join_token_worker.txt" 2>/dev/null || true

# Compress
echo "=== Compressing backup ==="
TAR_FILE="$BACKUP_DIR/${TIMESTAMP}.tar.gz"
tar -czf "$TAR_FILE" -C "$BACKUP_DIR" "$TIMESTAMP"
rm -rf "$BACKUP_PATH"

echo "=== Backup complete: $TAR_FILE ==="

# S3 sync (optional)
if [[ "$SYNC_S3" -eq 1 ]] && [[ -n "${BACKUP_S3_BUCKET:-}" ]]; then
    echo "=== Syncing to S3: $BACKUP_S3_BUCKET ==="
    if command -v aws &>/dev/null; then
        aws s3 cp "$TAR_FILE" "s3://$BACKUP_S3_BUCKET/backups/" 2>/dev/null || \
            echo "WARNING: S3 sync failed" >&2
    else
        echo "WARNING: aws cli not available, skipping S3 sync" >&2
    fi
fi

echo "Done."
