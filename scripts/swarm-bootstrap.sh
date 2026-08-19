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

   label-rabbitmq <node-name> <rabbitmq_id 1|2|3>
       Run on a manager, once per RabbitMQ node, after it has joined:
         docker node update --label-add rabbitmq_id=<id> <node-name>

   network
      Run on a manager, once: creates the attachable overlay network shared
      by all three stacks.

  secrets <pg-superuser-pass> <pg-replication-pass> <pg-app-pass>
      Run on a manager, once: creates the Swarm secrets consumed by
      docker-stack.postgres.yml. Generate strong random passwords, e.g.
      `openssl rand -base64 32`, do not reuse these examples.

  redis-secret <redis-password>
       Run on a manager, once: creates the redis_password Docker secret.
       Not read directly by docker-stack.redis.yml - it's the seed value
       `./scripts/vault-bootstrap.sh migrate-secrets` copies into Vault,
       from where redis/entrypoint.sh and redis/sentinel_entrypoint.sh
       fetch it at container start. Its value must match REDIS_PASSWORD
       passed to docker-stack.app.yml.

  rabbitmq-secret <rabbitmq-password>
       Run on a manager, once: creates the rabbitmq_password and
       rabbitmq_erlang_cookie Docker secrets. rabbitmq_password is not read
       directly by docker-stack.rabbitmq.yml - it's the seed value
       `./scripts/vault-bootstrap.sh migrate-secrets` copies into Vault,
       from where rabbitmq/entrypoint.sh and rabbitmq/join_entrypoint.sh
       fetch it at container start (rabbitmq_erlang_cookie stays a native
       Swarm secret, mounted directly). Its password value must match
       RABBITMQ_PASS passed to docker-stack.app.yml.

  app-secrets <jwt-secret> <encryption-key> <token-hash-key> [maintenance-token]
      Run on a manager, once: creates the jwt_secret, encryption_key,
      token_hash_key and (optionally) maintenance_token secrets consumed by
      the api service in docker-stack.app.yml (instead of passing
      JWT_SECRET/ENCRYPTION_KEY/TOKEN_HASH_KEY/MAINTENANCE_TOKEN as plain
      deploy-time env vars). Generate the first two with
      `openssl rand -base64 48` and the last two with `openssl rand -hex 32`;
      do not reuse examples. maintenance_token gates /api/maintenance/pre-update.

  haproxy-config
       DEPRECATED: haproxy configs are now auto-created by
       scripts/swarm-deploy.sh using content-hash naming. This command
       is kept for backward compatibility but no longer needed.
         docker config create haproxy_pg_cfg haproxy/haproxy.cfg

   haproxy-app-config
       DEPRECATED: see haproxy-config above.
         docker config create haproxy_app_cfg haproxy/haproxy-app.cfg

   redis-sentinel-config
        DEPRECATED: redis sentinel config is now auto-created by
        scripts/swarm-deploy.sh using content-hash naming.
          docker config create redis_sentinel_cfg redis/sentinel.conf.tpl

   haproxy-rabbitmq-config
       DEPRECATED: see haproxy-config above.
         docker config create haproxy_rabbitmq_cfg haproxy/haproxy-rabbitmq.cfg


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

  label-rabbitmq)
    node="${2:?usage: label-rabbitmq <node-name> <rabbitmq_id>}"
    rmqid="${3:?usage: label-rabbitmq <node-name> <rabbitmq_id>}"
    docker node update --label-add "rabbitmq_id=${rmqid}" "$node"
    ;;

  network)
    docker network create -d overlay --attachable --opt encrypted syslog_net
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

  rabbitmq-secret)
    pass="${2:?usage: rabbitmq-secret <rabbitmq-password>}"
    printf '%s' "$pass" | docker secret create rabbitmq_password -
    printf '%s' "$(openssl rand -base64 32)" | docker secret create rabbitmq_erlang_cookie -
    echo "Created rabbitmq_password, rabbitmq_erlang_cookie."
    echo "Remember: rabbitmq_password's value must match RABBITMQ_PASS used when deploying docker-stack.app.yml."
    ;;

  app-secrets)
    jwt="${2:?usage: app-secrets <jwt-secret> <encryption-key> <token-hash-key> [maintenance-token]}"
    enc="${3:?usage: app-secrets <jwt-secret> <encryption-key> <token-hash-key> [maintenance-token]}"
    thk="${4:?usage: app-secrets <jwt-secret> <encryption-key> <token-hash-key> [maintenance-token]}"
    printf '%s' "$jwt" | docker secret create jwt_secret -
    printf '%s' "$enc" | docker secret create encryption_key -
    printf '%s' "$thk" | docker secret create token_hash_key -
    if [[ -n "${5:-}" ]]; then
        printf '%s' "$5" | docker secret create maintenance_token -
        echo "Created jwt_secret, encryption_key, token_hash_key, maintenance_token."
    else
        echo "Created jwt_secret, encryption_key, token_hash_key (no maintenance_token passed - /api/maintenance/pre-update stays network-isolated)."
    fi
    ;;

  haproxy-config)
    if docker config inspect haproxy_pg_cfg >/dev/null 2>&1; then
        ts=$(date +%s)
        docker config create "haproxy_pg_cfg_${ts}" haproxy/haproxy.cfg
        echo "Config changed: created haproxy_pg_cfg_${ts}."
        echo "Update docker-stack.postgres.yml's haproxy config source to haproxy_pg_cfg_${ts}, then:"
        echo "  docker stack deploy -c docker-stack.postgres.yml logmara-pg"
    else
        docker config create haproxy_pg_cfg haproxy/haproxy.cfg
    fi
    ;;

  haproxy-app-config)
    if docker config inspect haproxy_app_cfg >/dev/null 2>&1; then
        ts=$(date +%s)
        docker config create "haproxy_app_cfg_${ts}" haproxy/haproxy-app.cfg
        echo "Config changed: created haproxy_app_cfg_${ts}."
        echo "Update docker-stack.app.yml's haproxy_app_cfg config source to haproxy_app_cfg_${ts}, then:"
        echo "  docker stack deploy -c docker-stack.app.yml logmara-app"
    else
        docker config create haproxy_app_cfg haproxy/haproxy-app.cfg
    fi
    ;;

  redis-sentinel-config)
    if docker config inspect redis_sentinel_cfg >/dev/null 2>&1; then
        ts=$(date +%s)
        docker config create "redis_sentinel_cfg_${ts}" redis/sentinel.conf.tpl
        echo "Config changed: created redis_sentinel_cfg_${ts}."
        echo "Update docker-stack.redis.yml's sentinel config source to redis_sentinel_cfg_${ts}, then:"
        echo "  docker stack deploy -c docker-stack.redis.yml logmara-redis"
    else
        docker config create redis_sentinel_cfg redis/sentinel.conf.tpl
    fi
    ;;

  haproxy-rabbitmq-config)
    if docker config inspect haproxy_rabbitmq_cfg >/dev/null 2>&1; then
        ts=$(date +%s)
        docker config create "haproxy_rabbitmq_cfg_${ts}" haproxy/haproxy-rabbitmq.cfg
        echo "Config changed: created haproxy_rabbitmq_cfg_${ts}."
        echo "Update docker-stack.rabbitmq.yml's haproxy config source to haproxy_rabbitmq_cfg_${ts}, then:"
        echo "  docker stack deploy -c docker-stack.rabbitmq.yml logmara-rabbitmq"
    else
        docker config create haproxy_rabbitmq_cfg haproxy/haproxy-rabbitmq.cfg
    fi
    ;;

  *)
    usage
    exit 1
    ;;
esac
