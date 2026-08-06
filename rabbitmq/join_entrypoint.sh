#!/bin/sh
# Entrypoint for RabbitMQ cluster join nodes (rabbitmq2, rabbitmq3).
# Generates config from template, starts the server, and joins the cluster.
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
# with the cookie from the secret.
rm -rf /var/lib/rabbitmq/mnesia/*
rm -f /var/lib/rabbitmq/.erlang.cookie

# Copy the Erlang cookie from the secret explicitly.
# join_entrypoint.sh bypasses the official docker-entrypoint.sh so
# RABBITMQ_ERLANG_COOKIE_FILE is never processed. tr -d '\n' strips
# any trailing newline from the secret file.
if [ -f /run/secrets/rabbitmq_erlang_cookie ]; then
  tr -d '\n' < /run/secrets/rabbitmq_erlang_cookie > /var/lib/rabbitmq/.erlang.cookie
  chmod 600 /var/lib/rabbitmq/.erlang.cookie
fi

# Start the server in the background
rabbitmq-server -detached

# Wait until this node is ready
echo "Waiting for RabbitMQ node to start..."
for i in $(seq 1 60); do
  if rabbitmq-diagnostics -q check_running 2>/dev/null; then
    break
  fi
  sleep 2
done

# Give rabbitmq1 time to be fully booted.
# depends_on in Docker Swarm does not wait for healthcheck, so we
# need to wait explicitly. rabbitmq1 started first so it should be
# ready after this delay.
echo "Waiting for rabbit@rabbitmq1 to be available..."
sleep 30

# Join the cluster with retries
echo "Joining cluster with rabbit@rabbitmq1..."
for attempt in $(seq 1 5); do
  if rabbitmqctl stop_app && rabbitmqctl join_cluster rabbit@rabbitmq1 && rabbitmqctl start_app; then
    echo "Successfully joined cluster"
    break
  fi
  echo "Join attempt $attempt failed, retrying in 10s..."
  sleep 10
done

# Keep the container running by tailing the log
tail -f /var/log/rabbitmq/*.log 2>/dev/null || while true; do sleep 60; done
