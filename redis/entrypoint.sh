#!/bin/sh
# Entrypoint for Redis data nodes (redis1/2/3). Fetches redis_password
# straight from Vault's HTTP API (same direct-API mechanism api and Patroni
# use - see patroni/entrypoint.sh and backend/vaultclient). vault_agent_token
# is a Swarm secret mounted at /run/secrets/vault_agent_token - it's only
# used to authenticate the Vault API call, never stored on disk itself.
set -e

vault_wait_unsealed() {
    interval=5
    timeout=300
    elapsed=0
    while [ "$elapsed" -lt "$timeout" ]; do
        resp=$(wget -q -O - \
            --header "X-Vault-Token: $(cat /run/secrets/vault_agent_token)" \
            "${VAULT_ADDR}/v1/sys/seal-status" 2>/dev/null) || true
        case "$resp" in
            *'"sealed":false'*)
                echo "vault is unsealed, proceeding"
                return 0
                ;;
        esac
        echo "vault is sealed, waiting ${interval}s (elapsed: ${elapsed}s / ${timeout}s)"
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    echo "vault did not become unsealed within ${timeout}s" >&2
    exit 1
}

# Extracts the "value" field Vault nests at .data.data.value for a KV v2
# read (jq/python3 aren't guaranteed to be present in the stock redis:7-alpine
# image, so this is a plain string match rather than real JSON parsing - safe
# here because every secret we write is a single-field {"value": "..."} object).
vault_fetch() {
    name="$1"
    resp=$(wget -q -O - \
        --header "X-Vault-Token: $(cat /run/secrets/vault_agent_token)" \
        "${VAULT_ADDR}/v1/secret/data/logmara/${name}" 2>/dev/null) || true
    [ -n "$resp" ] && printf '%s' "$resp" | sed -n 's/.*"data":{"data":{"value":"\([^"]*\)".*/\1/p'
}

vault_wait_unsealed

REDIS_PASSWORD=$(vault_fetch redis_password)
if [ -z "$REDIS_PASSWORD" ]; then
    echo "ERROR: failed to fetch redis_password from Vault" >&2
    exit 1
fi

# --masterauth is harmless on redis1 (the initial master, not itself a
# replica of anything) - it's only consulted when this node acts as a
# replica, which redis2/3's --replicaof arg (passed via "$@") is what
# actually triggers.
exec redis-server --requirepass "$REDIS_PASSWORD" --masterauth "$REDIS_PASSWORD" "$@"
