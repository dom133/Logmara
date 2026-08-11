#!/bin/sh
# Entrypoint for Redis Sentinel nodes (sentinel1/2/3). Fetches redis_password
# straight from Vault's HTTP API (same mechanism as redis/entrypoint.sh -
# see that file's header comment for why), appends it as sentinel's
# auth-pass, then hands off to redis-sentinel.
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

cp /etc/sentinel-template/sentinel.conf /tmp/sentinel.conf
echo "sentinel auth-pass mymaster $REDIS_PASSWORD" >> /tmp/sentinel.conf
exec redis-sentinel /tmp/sentinel.conf
