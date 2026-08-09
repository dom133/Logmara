#!/usr/bin/env bash
#
# Builds (and optionally pushes) all Docker images, reading REGISTRY and TAG
# from .env instead of requiring manual -t / -r flags.
#
# Usage:
#   ./scripts/build-images.sh [-p PLATFORMS] [-s] [-S] [-r REGISTRY] [-t TAG]
#
#   -p PLATFORMS  Comma-separated buildx platforms (e.g. "linux/amd64,linux/arm64")
#   -s            Skip push (build locally only)
#   -S            Skip Trivy vulnerability scan
#   -r REGISTRY  Override REGISTRY (default: read from .env)
#   -t TAG       Override TAG (default: read from .env)
#
# .env is loaded from the repo root. REGISTRY and TAG can still be overridden
# on the command line.
#
# Examples:
#   ./scripts/build-images.sh                     # build + push, REGISTRY/TAG from .env
#   ./scripts/build-images.sh -s                   # build only, no push
#   ./scripts/build-images.sh -t v2                # override tag
#   ./scripts/build-images.sh -r myreg.io/myuser   # override registry
#   ./scripts/build-images.sh -p linux/amd64,linux/arm64  # multi-arch

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ---------------------------------------------------------------------------
# Load .env
# ---------------------------------------------------------------------------
if [[ -f "$REPO_ROOT/.env" ]]; then
    set -a
    # shellcheck disable=SC1091
    source "$REPO_ROOT/.env"
    set +a
    echo "Loaded .env from: $REPO_ROOT/.env"
else
    echo "Warning: no .env found at $REPO_ROOT/.env — REGISTRY and TAG must be passed via -r / -t." >&2
fi

# ---------------------------------------------------------------------------
# Parse flags
# ---------------------------------------------------------------------------
PLATFORMS=""
SKIP_PUSH=0
SKIP_SCAN=0

while getopts "p:sSr:t:" opt; do
    case "$opt" in
        p) PLATFORMS="$OPTARG" ;;
        s) SKIP_PUSH=1 ;;
        S) SKIP_SCAN=1 ;;
        r) REGISTRY="$OPTARG" ;;
        t) TAG="$OPTARG" ;;
        *) echo "Usage: $0 [-p PLATFORMS] [-s] [-S] [-r REGISTRY] [-t TAG]" >&2; exit 1 ;;
    esac
done

REGISTRY="${REGISTRY:?REGISTRY is required (set in .env or pass -r)}"
TAG="${TAG:-latest}"

echo "Registry: $REGISTRY"
echo "Tag:      $TAG"

# ---------------------------------------------------------------------------
# Image list
# ---------------------------------------------------------------------------
declare -A IMAGES=(
    [logmara-api]="Dockerfile.backend"
    [logmara-frontend]="Dockerfile.frontend"
    [logmara-patroni]="Dockerfile.patroni"
    [logmara-rsyslog]="Dockerfile.rsyslog"
    [logmara-rsyslog-relay]="Dockerfile.rsyslog-relay"
)

for name in "${!IMAGES[@]}"; do
    dockerfile="${IMAGES[$name]}"
    dockerfile_path="$REPO_ROOT/$dockerfile"
    full_tag="$REGISTRY/$name:$TAG"

    if [[ ! -f "$dockerfile_path" ]]; then
        echo "Skipping $name: $dockerfile_path not found" >&2
        continue
    fi

    if [[ -n "$PLATFORMS" ]]; then
        echo
        echo "=== Building $full_tag (platforms: $PLATFORMS) ==="
        if [[ "$SKIP_PUSH" -eq 1 ]]; then
            docker buildx build -f "$dockerfile_path" -t "$full_tag" --platform "$PLATFORMS" "$REPO_ROOT"
        else
            docker buildx build -f "$dockerfile_path" -t "$full_tag" --platform "$PLATFORMS" --push "$REPO_ROOT"
        fi
    else
        echo
        echo "=== Building $full_tag ==="
        docker build -f "$dockerfile_path" -t "$full_tag" "$REPO_ROOT"

        if [[ "$SKIP_PUSH" -ne 1 ]]; then
            echo "=== Pushing $full_tag ==="
            docker push "$full_tag"
        fi
    fi

    # Trivy scan
    if [[ "$SKIP_SCAN" -eq 0 ]]; then
        echo "=== Trivy scanning $full_tag ==="
        if ! bash "$REPO_ROOT/scripts/trivy-scan.sh" -i "$full_tag"; then
            echo "FAIL: $full_tag has CRITICAL/HIGH vulnerabilities, aborting." >&2
            exit 1
        fi
    fi
done

echo
echo "Done."
