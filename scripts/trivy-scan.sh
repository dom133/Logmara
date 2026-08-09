#!/usr/bin/env bash
#
# Scans all Dockerfiles with Trivy for CRITICAL/HIGH vulnerabilities.
# Called by build-and-push.sh or run standalone.
#
# Usage:
#   ./scripts/trivy-scan.sh [-i IMAGE]
#
#   -i IMAGE  Scan a single image (e.g. dom133/logmara-api:latest)
#             If omitted, scans all images listed in build-and-push.sh.
#
# Exit codes:
#   0 - clean
#   1 - CRITICAL/HIGH vulnerabilities found
#   2 - trivy not available
#

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

IMAGE=""
while getopts "i:" opt; do
    case "$opt" in
        i) IMAGE="$OPTARG" ;;
        *) echo "Usage: $0 [-i IMAGE]" >&2; exit 1 ;;
    esac
done

if ! command -v trivy &>/dev/null; then
    echo "ERROR: trivy not found. Install from https://github.com/aquasecurity/trivy" >&2
    exit 2
fi

FAILURE=0

scan_image() {
    local img="$1"
    echo "=== Scanning $img ==="
    if ! trivy image --severity CRITICAL,HIGH --ignorefile "$REPO_ROOT/.trivyignore" "$img" 2>&1; then
        echo "FAIL: $img has CRITICAL/HIGH vulnerabilities" >&2
        FAILURE=1
    else
        echo "OK: $img clean"
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
            echo "=== Scanning Dockerfile: $name ==="
            if ! trivy build --severity CRITICAL,HIGH --ignorefile "$REPO_ROOT/.trivyignore" -f "$dockerfile" "$REPO_ROOT" 2>&1; then
                echo "FAIL: $name has CRITICAL/HIGH vulnerabilities" >&2
                FAILURE=1
            else
                echo "OK: $name clean"
            fi
            echo
        fi
    done
fi

if [[ "$FAILURE" -ne 0 ]]; then
    echo "Aborting: one or more images have CRITICAL/HIGH vulnerabilities." >&2
    exit 1
fi

echo "All images passed Trivy scan."
