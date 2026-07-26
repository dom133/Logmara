#!/bin/bash
# Helper for standing up the Docker Swarm cluster the HA stacks
# (docker-stack.postgres.yml, docker-stack.redis.yml, docker-stack.app.yml)
# deploy onto. Run the relevant subcommand on the relevant node — this is a
# guide, not a single unattended script, since it spans multiple
# physical/virtual machines.
#
# See README "High Availability Deployment" for the full walkthrough.
set -euo pipefail

usage() {
    cat <<'EOF'
Usage: swarm-bootstrap.sh <command> [args]

Commands (run on the indicated node):

  init-manager <advertise-ip>
      Run ONCE, on the first manager node. Initializes the swarm and prints
      the join tokens/commands for the other nodes.

  label-pg <node-name> <pg_id 1|2|3>
      Run on a manager, once per Postgres node, after it has joined:
        docker node update --label-add pg_id=<id> <node-name>

  label-cache <node-name> <cache_id 1|2|3>
      Run on a manager, once per Redis node, after it has joined:
        docker node update --label-add cache_id=<id> <node-name>

  label-app <node-name>
      Run on a manager, once per node that will run api/frontend/haproxy.
      app=true and edge=true are independent labels (not one "role" value),
      so the same node can get both:
        docker node update --label-add app=true <node-name>

  label-edge <node-name>
      Run on a manager, once per node that will run rsyslog + keepalived
      (can be the same physical nodes as label-app on small clusters):
        docker node update --label-add edge=true <node-name>

  network
      Run on a manager, once: creates the attachable overlay network shared
      by all three stacks.

  secrets <pg-superuser-pass> <pg-replication-pass> <pg-app-pass>
      Run on a manager, once: creates the Swarm secrets consumed by
      docker-stack.postgres.yml. Generate strong random passwords, e.g.
      `openssl rand -base64 32`, do not reuse these examples.

  redis-secret <redis-password>
      Run on a manager, once: creates the redis_password secret consumed by
      docker-stack.redis.yml. Its value must match REDIS_PASSWORD passed to
      docker-stack.app.yml.

  app-secrets <jwt-secret> <encryption-key>
      Run on a manager, once: creates the jwt_secret and encryption_key
      secrets consumed by the api service in docker-stack.app.yml (instead
      of passing JWT_SECRET/ENCRYPTION_KEY as plain deploy-time env vars).
      Generate both with `openssl rand -base64 48`, do not reuse examples.

  haproxy-config
      Run on a manager, once (and again any time haproxy/haproxy.cfg
      changes — configs are immutable, so this recreates it with a new
      name if it already exists and prints the docker service update
      command to roll it out):
        docker config create haproxy_pg_cfg haproxy/haproxy.cfg

  redis-sentinel-config
      Same idea as haproxy-config, for redis/sentinel.conf.tpl:
        docker config create redis_sentinel_cfg redis/sentinel.conf.tpl

EOF
}

cmd="${1:-}"
case "$cmd" in
  init-manager)
    ip="${2:?usage: init-manager <advertise-ip>}"
    docker swarm init --advertise-addr "$ip"
    echo
    echo "Manager join token (for more managers, quorum-critical, use sparingly):"
    docker swarm join-token manager -q
    echo
    echo "Worker join token:"
    docker swarm join-token worker -q
    ;;

  label-pg)
    node="${2:?usage: label-pg <node-name> <pg_id>}"
    pgid="${3:?usage: label-pg <node-name> <pg_id>}"
    docker node update --label-add "pg_id=${pgid}" "$node"
    ;;

  label-cache)
    node="${2:?usage: label-cache <node-name> <cache_id>}"
    cacheid="${3:?usage: label-cache <node-name> <cache_id>}"
    docker node update --label-add "cache_id=${cacheid}" "$node"
    ;;

  label-app)
    node="${2:?usage: label-app <node-name>}"
    docker node update --label-add app=true "$node"
    ;;

  label-edge)
    node="${2:?usage: label-edge <node-name>}"
    docker node update --label-add edge=true "$node"
    ;;

  network)
    docker network create -d overlay --attachable syslog_net
    ;;

  secrets)
    su="${2:?usage: secrets <pg-superuser-pass> <pg-replication-pass> <pg-app-pass>}"
    rep="${3:?usage: secrets <pg-superuser-pass> <pg-replication-pass> <pg-app-pass>}"
    app="${4:?usage: secrets <pg-superuser-pass> <pg-replication-pass> <pg-app-pass>}"
    printf '%s' "$su"  | docker secret create pg_superuser_password -
    printf '%s' "$rep" | docker secret create pg_replication_password -
    printf '%s' "$app" | docker secret create pg_app_password -
    echo "Created pg_superuser_password, pg_replication_password, pg_app_password."
    echo "Remember: pg_app_password's value must match POSTGRES_PASSWORD used when deploying docker-stack.app.yml."
    ;;

  redis-secret)
    pass="${2:?usage: redis-secret <redis-password>}"
    printf '%s' "$pass" | docker secret create redis_password -
    echo "Created redis_password."
    echo "Remember: its value must match REDIS_PASSWORD used when deploying docker-stack.app.yml."
    ;;

  app-secrets)
    jwt="${2:?usage: app-secrets <jwt-secret> <encryption-key>}"
    enc="${3:?usage: app-secrets <jwt-secret> <encryption-key>}"
    printf '%s' "$jwt" | docker secret create jwt_secret -
    printf '%s' "$enc" | docker secret create encryption_key -
    echo "Created jwt_secret, encryption_key."
    ;;

  haproxy-config)
    if docker config inspect haproxy_pg_cfg >/dev/null 2>&1; then
        ts=$(date +%s)
        docker config create "haproxy_pg_cfg_${ts}" haproxy/haproxy.cfg
        echo "Config changed: created haproxy_pg_cfg_${ts}."
        echo "Update docker-stack.postgres.yml's haproxy config source to haproxy_pg_cfg_${ts}, then:"
        echo "  docker stack deploy -c docker-stack.postgres.yml syslytics-pg"
    else
        docker config create haproxy_pg_cfg haproxy/haproxy.cfg
    fi
    ;;

  redis-sentinel-config)
    if docker config inspect redis_sentinel_cfg >/dev/null 2>&1; then
        ts=$(date +%s)
        docker config create "redis_sentinel_cfg_${ts}" redis/sentinel.conf.tpl
        echo "Config changed: created redis_sentinel_cfg_${ts}."
        echo "Update docker-stack.redis.yml's sentinel config source to redis_sentinel_cfg_${ts}, then:"
        echo "  docker stack deploy -c docker-stack.redis.yml syslytics-redis"
    else
        docker config create redis_sentinel_cfg redis/sentinel.conf.tpl
    fi
    ;;

  *)
    usage
    exit 1
    ;;
esac
