#!/bin/sh
# Replaces __RABBITMQ_PASSWORD__ in rabbitmq.conf.tpl with the actual
# password, then hands off to the official RabbitMQ entrypoint.
# Tries /run/secrets/rabbitmq_password first (Swarm), falls back to
# RABBITMQ_PASS env var (docker-compose).
set -e

CONF="/etc/rabbitmq/rabbitmq.conf"
TPL="/etc/rabbitmq/rabbitmq.conf.tpl"

if [ -f "$TPL" ]; then
  PASSWORD=""
  if [ -f /run/secrets/rabbitmq_password ]; then
    PASSWORD=$(cat /run/secrets/rabbitmq_password)
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
