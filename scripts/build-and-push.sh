#!/usr/bin/env bash
#
# Builds and pushes all syslog_gui Docker images to Docker Hub under the "logmara" namespace.
#
# Usage:
#   ./scripts/build-and-push.sh [-t TAG] [-p PLATFORMS] [-s]
#
#   -t TAG        Tag to apply to each image (default: latest)
#   -p PLATFORMS  Comma-separated buildx platforms for a multi-arch build
#                 (e.g. "linux/amd64,linux/arm64"). Builds & pushes via buildx.
#   -s            Skip push (build locally only)
#
# Examples:
#   ./scripts/build-and-push.sh
#   ./scripts/build-and-push.sh -t v1.2.0
#   ./scripts/build-and-push.sh -t v1.2.0 -p linux/amd64,linux/arm64

set -euo pipefail

DOCKERHUB_USER="logmara"
TAG="latest"
PLATFORMS=""
SKIP_PUSH=0

while getopts "t:p:s" opt; do
    case "$opt" in
        t) TAG="$OPTARG" ;;
        p) PLATFORMS="$OPTARG" ;;
        s) SKIP_PUSH=1 ;;
        *) echo "Usage: $0 [-t TAG] [-p PLATFORMS] [-s]" >&2; exit 1 ;;
    esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

declare -A IMAGES=(
    [syslog-gui-backend]="Dockerfile.backend"
    [syslog-gui-frontend]="Dockerfile.frontend"
    [syslog-gui-patroni]="Dockerfile.patroni"
    [syslog-gui-rsyslog]="Dockerfile.rsyslog"
    [syslog-gui-rsyslog-relay]="Dockerfile.rsyslog-relay"
)

echo "Docker Hub namespace: $DOCKERHUB_USER"
echo "Tag: $TAG"

for name in "${!IMAGES[@]}"; do
    dockerfile="${IMAGES[$name]}"
    dockerfile_path="$REPO_ROOT/$dockerfile"
    full_tag="$DOCKERHUB_USER/$name:$TAG"

    if [[ ! -f "$dockerfile_path" ]]; then
        echo "Skipping $name: $dockerfile_path not found" >&2
        continue
    fi

    if [[ -n "$PLATFORMS" ]]; then
        echo
        echo "=== Building & pushing $full_tag (platforms: $PLATFORMS) ==="
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
done

echo
echo "Done."
