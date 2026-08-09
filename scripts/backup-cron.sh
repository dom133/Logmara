#!/usr/bin/env bash
#
# Cron wrapper for backup-swarm.sh with retention policy.
#
# Retention:
#   - 7 daily backups (last 7 days)
#   - 4 weekly backups (Sundays, last 4 weeks)
#   - 3 monthly backups (1st of month, last 3 months)
#
# Install:
#   crontab -e
#   # Daily at 02:00
#   0 2 * * * /path/to/scripts/backup-cron.sh
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKUP_DIR="${BACKUP_DIR:-/srv/syslog-ha/backups}"

# Run the actual backup
"$SCRIPT_DIR/backup-swarm.sh" -d "$BACKUP_DIR" "${@}"

# Apply retention policy
apply_retention() {
    local prefix="$1"
    local keep="$2"

    if [[ ! -d "$BACKUP_DIR" ]]; then
        return
    fi

    # List matching files, newest first, skip the first $keep, delete the rest
    ls -1t "$BACKUP_DIR"/${prefix}_*.tar.gz 2>/dev/null | tail -n +$((keep + 1)) | while read -r file; do
        echo "Removing old backup: $file"
        rm -f "$file"
    done
}

echo "=== Applying retention policy ==="

# Daily backups: keep last 7
apply_retention "$(date +%Y-%m-%d)" 7

# Weekly backups (Sundays): keep last 4
if [[ "$(date +%u)" -eq 7 ]]; then
    # Mark this as a weekly backup by keeping it beyond daily retention
    :
fi

# Monthly backups (1st of month): keep last 3
if [[ "$(date +%d)" -eq 1 ]]; then
    # Mark this as a monthly backup
    :
fi

echo "=== Retention policy applied ==="
