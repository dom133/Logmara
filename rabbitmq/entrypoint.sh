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

exec docker-entrypoint.sh rabbitmq-server
