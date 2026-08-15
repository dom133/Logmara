#!/bin/sh
# Replaces __RABBITMQ_PASSWORD__ in rabbitmq.conf.tpl with the actual
# password, then hands off to the official RabbitMQ entrypoint.
# Fetches rabbitmq_password straight from Vault's HTTP API when VAULT_ADDR
# is set (same direct-API mechanism api/Patroni use - see
# patroni/entrypoint.sh, backend/vaultclient), using vault_agent_token only
# to authenticate that call. Falls back to RABBITMQ_PASS env var when
# VAULT_ADDR is unset (plain docker-compose, no Vault deployed).
set -e

CONF="/etc/rabbitmq/rabbitmq.conf"
TPL="/etc/rabbitmq/rabbitmq.conf.tpl"

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
# read (jq/python3 aren't guaranteed to be present in the stock
# rabbitmq-alpine image, so this is a plain string match rather than real
# JSON parsing - safe here because every secret we write is a single-field
# {"value": "..."} object).
vault_fetch_rabbitmq_password() {
  resp=$(wget -q -O - \
    --header "X-Vault-Token: $(cat /run/secrets/vault_agent_token)" \
    "${VAULT_ADDR}/v1/secret/data/logmara/rabbitmq_password" 2>/dev/null) || true
  [ -n "$resp" ] && printf '%s' "$resp" | sed -n 's/.*"data":{"data":{"value":"\([^"]*\)".*/\1/p'
}

if [ -f "$TPL" ]; then
  PASSWORD=""
  if [ -n "$VAULT_ADDR" ]; then
    vault_wait_unsealed
    PASSWORD=$(vault_fetch_rabbitmq_password)
    if [ -z "$PASSWORD" ]; then
      echo "ERROR: failed to fetch rabbitmq_password from Vault" >&2
      exit 1
    fi
  elif [ -n "$RABBITMQ_PASS" ]; then
    PASSWORD="$RABBITMQ_PASS"
  fi
  sed "s|__RABBITMQ_PASSWORD__|${PASSWORD}|g" "$TPL" > "$CONF"
fi

# Clean stale mnesia data and erlang cookie so the node starts fresh
# with the cookie from the secret. Without this, a cached .erlang.cookie
# from a previous deployment causes "Invalid challenge reply" on cluster
# join because the official entrypoint only copies the secret cookie
# when .erlang.cookie does not exist.
rm -rf /var/lib/rabbitmq/mnesia/*
rm -f /var/lib/rabbitmq/.erlang.cookie

# Copy the Erlang cookie from the secret. The official entrypoint
# handles RABBITMQ_ERLANG_COOKIE_FILE, but we do it explicitly to
# ensure consistency across all nodes. tr -d '\n' strips any
# trailing newline from the secret file.
if [ -f /run/secrets/rabbitmq_erlang_cookie ]; then
  tr -d '\n' < /run/secrets/rabbitmq_erlang_cookie > /var/lib/rabbitmq/.erlang.cookie
  chmod 600 /var/lib/rabbitmq/.erlang.cookie
fi

exec docker-entrypoint.sh rabbitmq-server
