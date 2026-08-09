#!/usr/bin/env bash
#
# Full backup of Docker Swarm state: etcd snapshot, Postgres dump, configs,
# secrets metadata, stack YAMLs, and join tokens.
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

# 1. etcd snapshot (run on a manager node)
echo "[1/6] etcd snapshot..."
ETCDCTL_API=3 etcdctl snapshot save "$BACKUP_PATH/etcd.snapshot" 2>/dev/null || \
    echo "WARNING: etcd snapshot failed (not a manager node or etcdctl unavailable)" >&2

# 2. Postgres dump (via haproxy:5000)
echo "[2/6] Postgres dump..."
PGPASSWORD="$(cat /run/secrets/pg_superuser_password 2>/dev/null || echo '')" \
    pg_dump -h haproxy -p 5000 -U syslog -d syslog_db --format=custom --compress=9 \
        -f "$BACKUP_PATH/postgres.dump" 2>/dev/null || \
    echo "WARNING: pg_dump failed (pg_dump unavailable or DB not reachable)" >&2

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
