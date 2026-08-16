#!/usr/bin/env bash
# Runs the same checks as .github/workflows/ci.yml locally, so lint/build
# errors are caught before pushing instead of in the CI run.
#
# Usage:
#   ./scripts/ci-check.sh
#   ./scripts/ci-check.sh --skip-tests
#   ./scripts/ci-check.sh --skip-frontend
#
# Flags:
#   --skip-backend   Skip the Go checks (vet, staticcheck, build, test).
#   --skip-frontend  Skip the frontend checks (lint, tsc, build, test).
#   --skip-tests     Skip `go test` / `npm test` (keeps vet/staticcheck/lint/build/tsc,
#                    which are the fast, most commonly broken checks).

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SKIP_BACKEND=0
SKIP_FRONTEND=0
SKIP_TESTS=0
FAILED=()

for arg in "$@"; do
    case "$arg" in
        --skip-backend) SKIP_BACKEND=1 ;;
        --skip-frontend) SKIP_FRONTEND=1 ;;
        --skip-tests) SKIP_TESTS=1 ;;
        *)
            echo "Unknown flag: $arg" >&2
            exit 2
            ;;
    esac
done

CYAN='\033[0;36m'
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
NC='\033[0m'

run_step() {
    local name="$1" workdir="$2"
    shift 2
    echo ""
    echo -e "${CYAN}==> ${name}${NC}"
    if (cd "$workdir" && "$@"); then
        echo -e "${GREEN}    OK: ${name}${NC}"
    else
        echo -e "${RED}    FAILED: ${name}${NC}"
        FAILED+=("$name")
    fi
}

if [[ "$SKIP_BACKEND" -eq 0 ]]; then
    BACKEND="$REPO_ROOT/backend"

    run_step "go vet ./..." "$BACKEND" go vet ./...

    if ! command -v staticcheck >/dev/null 2>&1; then
        echo -e "${YELLOW}==> staticcheck not found, installing (go install honnef.co/go/tools/cmd/staticcheck@latest)...${NC}"
        go install honnef.co/go/tools/cmd/staticcheck@latest
    fi
    run_step "staticcheck ./..." "$BACKEND" staticcheck ./...

    run_step "go build ./..." "$BACKEND" go build ./...

    if [[ "$SKIP_TESTS" -eq 0 ]]; then
        if [[ "$(go env CGO_ENABLED)" == "1" ]]; then
            run_step "go test -race -count=1 ./..." "$BACKEND" go test -race -count=1 ./...
        else
            echo -e "${YELLOW}    (CGO_ENABLED=0 / no C compiler found -- running without -race; CI still runs with -race on Linux)${NC}"
            run_step "go test -count=1 ./..." "$BACKEND" go test -count=1 ./...
        fi
    fi
fi

if [[ "$SKIP_FRONTEND" -eq 0 ]]; then
    FRONTEND="$REPO_ROOT/frontend"

    run_step "npm run lint" "$FRONTEND" npm run lint
    run_step "tsc --noEmit" "$FRONTEND" npx tsc --noEmit
    run_step "npm run build" "$FRONTEND" npm run build

    if [[ "$SKIP_TESTS" -eq 0 ]]; then
        run_step "npm test" "$FRONTEND" npm test
    fi
fi

echo ""
if [[ ${#FAILED[@]} -gt 0 ]]; then
    echo -e "${RED}FAILED steps: ${FAILED[*]}${NC}"
    exit 1
fi
echo -e "${GREEN}All checks passed.${NC}"
