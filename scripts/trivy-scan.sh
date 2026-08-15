#!/usr/bin/env bash
#
# Scans all Dockerfiles with Trivy for CRITICAL/HIGH vulnerabilities.
# Called by build-and-push.sh or run standalone.
#
# Usage:
#   ./scripts/trivy-scan.sh [-i IMAGE] [-o REPORT_DIR]
#
#   -i IMAGE       Scan a single image (e.g. dom133/logmara-api:latest)
#                  If omitted, scans all images listed in build-and-push.sh.
#   -o REPORT_DIR  Where to save scan reports (default: /srv/syslog-ha/trivy-reports,
#                  or $TRIVY_REPORT_DIR if set). One timestamped .txt file per
#                  image/Dockerfile scanned, with the same output shown on screen.
#
# Exit codes:
#   0 - clean
#   1 - CRITICAL/HIGH vulnerabilities found
#   2 - trivy not available
#

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

IMAGE=""
REPORT_DIR="${TRIVY_REPORT_DIR:-/srv/syslog-ha/trivy-reports}"
while getopts "i:o:" opt; do
    case "$opt" in
        i) IMAGE="$OPTARG" ;;
        o) REPORT_DIR="$OPTARG" ;;
        *) echo "Usage: $0 [-i IMAGE] [-o REPORT_DIR]" >&2; exit 1 ;;
    esac
done

if ! command -v trivy &>/dev/null; then
    echo "ERROR: trivy not found. Install from https://github.com/aquasecurity/trivy" >&2
    exit 2
fi

mkdir -p "$REPORT_DIR"
TIMESTAMP="$(date +%Y-%m-%d_%H%M%S)"

FAILURE=0

# Turns e.g. "10.1.10.31:5000/logmara/logmara-api:v20" into a filesystem-safe
# "10.1.10.31_5000_logmara_logmara-api_v20" report filename.
safe_name() {
    printf '%s' "$1" | tr '/:' '__'
}

scan_image() {
    local img="$1"
    local report_file="$REPORT_DIR/$(safe_name "$img")_${TIMESTAMP}.txt"
    echo "=== Scanning $img ==="
    if trivy image --severity CRITICAL,HIGH --ignorefile "$REPO_ROOT/.trivyignore" "$img" 2>&1 | tee "$report_file"; then
        echo "OK: $img clean (report: $report_file)"
    else
        echo "FAIL: $img has CRITICAL/HIGH vulnerabilities (report: $report_file)" >&2
        FAILURE=1
    fi
    echo
}

if [[ -n "$IMAGE" ]]; then
    scan_image "$IMAGE"
else
    declare -A IMAGES=(
        [logmara-api]="Dockerfile.backend"
        [logmara-frontend]="Dockerfile.frontend"
        [logmara-patroni]="Dockerfile.patroni"
        [logmara-rsyslog]="Dockerfile.rsyslog"
        [logmara-rsyslog-relay]="Dockerfile.rsyslog-relay"
    )

    for name in "${!IMAGES[@]}"; do
        dockerfile="${IMAGES[$name]}"
        if [[ -f "$REPO_ROOT/$dockerfile" ]]; then
            # Scan the Dockerfile for build-stage vulnerabilities
            report_file="$REPORT_DIR/$(safe_name "$name")_${TIMESTAMP}.txt"
            echo "=== Scanning Dockerfile: $name ==="
            if trivy build --severity CRITICAL,HIGH --ignorefile "$REPO_ROOT/.trivyignore" -f "$dockerfile" "$REPO_ROOT" 2>&1 | tee "$report_file"; then
                echo "OK: $name clean (report: $report_file)"
            else
                echo "FAIL: $name has CRITICAL/HIGH vulnerabilities (report: $report_file)" >&2
                FAILURE=1
            fi
            echo
        fi
    done
fi

echo "Reports saved to: $REPORT_DIR"

if [[ "$FAILURE" -ne 0 ]]; then
    echo "Aborting: one or more images have CRITICAL/HIGH vulnerabilities." >&2
    exit 1
fi

echo "All images passed Trivy scan."
