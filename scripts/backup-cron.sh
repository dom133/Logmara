#!/usr/bin/env bash
#
# Cron wrapper for backup-swarm.sh with retention policy.
#
# Retention (evaluated over all *.tar.gz files in BACKUP_DIR, using the
# date encoded in each backup's filename, YYYY-MM-DD_HHMMSS.tar.gz):
#   - 7 daily backups   (newest 7 backups, any day)
#   - 4 weekly backups  (newest 4 backups made on a Sunday)
#   - 3 monthly backups (newest 3 backups made on the 1st of the month)
# A backup is removed only if it falls outside all three windows.
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

echo "=== Applying retention policy ==="

if [[ -d "$BACKUP_DIR" ]]; then
    mapfile -t ALL_BACKUPS < <(ls -1t "$BACKUP_DIR"/*.tar.gz 2>/dev/null)

    if [[ "${#ALL_BACKUPS[@]}" -gt 0 ]]; then
        declare -A KEEP=()

        # Daily: newest 7 backups, regardless of day
        for f in "${ALL_BACKUPS[@]:0:7}"; do
            KEEP["$f"]=1
        done

        # Weekly: newest 4 Sunday backups
        count=0
        for f in "${ALL_BACKUPS[@]}"; do
            [[ "$count" -ge 4 ]] && break
            date_part="$(basename "$f" | cut -d_ -f1)"
            dow="$(date -d "$date_part" +%u 2>/dev/null || echo 0)"
            if [[ "$dow" == "7" ]]; then
                KEEP["$f"]=1
                count=$((count + 1))
            fi
        done

        # Monthly: newest 3 first-of-month backups
        count=0
        for f in "${ALL_BACKUPS[@]}"; do
            [[ "$count" -ge 3 ]] && break
            date_part="$(basename "$f" | cut -d_ -f1)"
            dom="$(date -d "$date_part" +%d 2>/dev/null || echo 00)"
            if [[ "$dom" == "01" ]]; then
                KEEP["$f"]=1
                count=$((count + 1))
            fi
        done

        for f in "${ALL_BACKUPS[@]}"; do
            if [[ -z "${KEEP[$f]:-}" ]]; then
                echo "Removing old backup: $f"
                rm -f "$f"
            fi
        done
    fi
fi

echo "=== Retention policy applied ==="
