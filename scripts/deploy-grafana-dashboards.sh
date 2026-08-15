#!/usr/bin/env bash
#
# Copies Grafana dashboard JSON files from the repo to the NFS mount,
# so the provisioned Grafana instance picks them up automatically.
#
# Usage:
#   ./scripts/deploy-grafana-dashboards.sh
#   ./scripts/deploy-grafana-dashboards.sh --env-file /path/to/.env
#
# The target NFS path is read from NFS_GRAFANA_DASHBOARDS_PATH (default:
# /srv/syslog-ha/nfs/grafana-dashboards). The variable is loaded from
# .env if present, or must be exported in the shell.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# ---------------------------------------------------------------------------
# Parse flags
# ---------------------------------------------------------------------------
ENV_FILE=""

while [[ "${1:-}" == --* ]]; do
    case "$1" in
        --env-file)
            ENV_FILE="${2:?--env-file requires a path}"
            shift 2
            ;;
        *)
            echo "Unknown flag: $1" >&2
            exit 1
            ;;
    esac
done

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

DASHBOARD_SRC="$REPO_ROOT/monitoring/grafana-dashboards"
DASHBOARD_DST="${NFS_GRAFANA_DASHBOARDS_PATH:-/srv/syslog-ha/nfs/grafana-dashboards}"

# ---------------------------------------------------------------------------
# Validate source
# ---------------------------------------------------------------------------
if [[ ! -d "$DASHBOARD_SRC" ]]; then
    echo "Error: dashboard source directory not found: $DASHBOARD_SRC" >&2
    exit 1
fi

DASHBOARD_COUNT=$(find "$DASHBOARD_SRC" -maxdepth 1 -name '*.json' -type f | wc -l)

if [[ "$DASHBOARD_COUNT" -eq 0 ]]; then
    echo "Warning: no .json dashboard files found in $DASHBOARD_SRC" >&2
    exit 0
fi

# ---------------------------------------------------------------------------
# Validate destination
# ---------------------------------------------------------------------------
if [[ ! -d "$DASHBOARD_DST" ]]; then
    echo "Error: NFS dashboard directory not found: $DASHBOARD_DST" >&2
    echo "Make sure the NFS mount is available and NFS_GRAFANA_DASHBOARDS_PATH is set correctly." >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# Copy dashboards
# ---------------------------------------------------------------------------
echo
echo "Syncing $DASHBOARD_COUNT dashboard(s) to $DASHBOARD_DST ..."

# Use rsync if available, fall back to cp
if command -v rsync &>/dev/null; then
    rsync -av --delete \
        --include='*.json' \
        --exclude='*' \
        "$DASHBOARD_SRC/" "$DASHBOARD_DST/"
else
    # Remove stale .json files in destination that no longer exist in source
    for f in "$DASHBOARD_DST"/*.json; do
        [[ -f "$f" ]] || continue
        basename_f="$(basename "$f")"
        if [[ ! -f "$DASHBOARD_SRC/$basename_f" ]]; then
            rm -f "$f"
            echo "  Removed stale: $basename_f"
        fi
    done

    # Copy new/updated dashboards
    for f in "$DASHBOARD_SRC"/*.json; do
        cp -f "$f" "$DASHBOARD_DST/"
        echo "  Copied: $(basename "$f")"
    done
fi

echo
echo "Done. Grafana will pick up the changes within ~30s (updateIntervalSeconds)."
