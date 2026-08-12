#!/usr/bin/env bash
#
# Recover a PostgreSQL/Patroni standby that can't catch up via streaming
# replication - symptoms in `docker service logs logmara-pg_postgres<N>`:
#   - "FATAL: could not receive data from WAL stream: ERROR: requested WAL
#     segment ... has already been removed"
#   - "LOG: waiting for WAL to become available at ..." repeating forever
#   - "FATAL: the database system is starting up" on every connection
#     attempt while the above two lines loop
#
# This happens when the standby was disconnected long enough that the
# leader already recycled the WAL segment it needs - streaming replication
# can never resume on its own at that point, no matter how long you wait,
# because the data it needs to send simply doesn't exist anymore.
#
# Unlike scripts/vault-recover-node.sh (which has to manually remove the
# raft peer and wipe the on-disk data dir itself), Patroni has a built-in
# REST endpoint for exactly this - `POST /reinitialize` on the affected
# member wipes its local pgdata and re-clones a fresh pg_basebackup from
# the current leader for us. This script just finds the leader, confirms
# the target isn't it (reinitializing the leader would be catastrophic),
# and drives that endpoint + waits for the standby to rejoin as a healthy
# streaming replica.
#
# Usage:
#   ./scripts/pg-recover-standby.sh <1|2|3> [--yes]
#
#   <1|2|3>   Which postgresN node to recover (must be a replica, not
#             the current leader)
#   --yes     Skip the confirmation prompt (for non-interactive use)
#
# Needs, run from a Swarm manager with `docker` CLI + `jq` access to the
# cluster.
#
set -euo pipefail

NODE="${1:-}"
CONFIRM="${2:-}"
CURL_IMAGE="curlimages/curl:8.10.1"
TIMEOUT_ITERS=40   # 40 * (8s sleep + ~5s helper overhead) ~= 8-9 minutes

if [[ ! "$NODE" =~ ^[123]$ ]]; then
    echo "Usage: $0 <1|2|3> [--yes]" >&2
    exit 1
fi

TARGET="postgres$NODE"
SERVICE="logmara-pg_postgres$NODE"

# ---------------------------------------------------------------------------
# Patroni REST API helper - unlike vault-recover-node.sh's vault_cli (a
# plain `docker run --network syslog_net`, which works fine because the
# vault services use the default VIP endpoint mode), postgres1/2/3 run
# with `endpoint_mode: dnsrr` (see docker-stack.postgres.yml). Resolving a
# dnsrr service's container hostname from a one-off container that isn't
# itself scheduled on the same node as the target is a known-flaky area of
# Docker's embedded DNS - it can silently fail to connect (curl reports
# HTTP 000) even though the target container is perfectly healthy. So
# instead we pin the helper to the target's own node via the same
# node.labels.pg_id constraint docker-stack.postgres.yml uses to place
# postgresN itself, the same trick vault-recover-node.sh uses (there, to
# reach a bind-mounted host dir; here, to make the DNS lookup local).
# ---------------------------------------------------------------------------
# Output is always "<body>\n<http_code>|<curl_errormsg>" so callers can
# tell a real HTTP response (2xx/4xx/5xx) apart from a connection that
# never happened at all (000, with a reason: DNS failure, connection
# refused, timeout, ...) instead of just seeing an opaque empty response.
patroni_curl() {
    local n="$1"; shift
    local svc="pg-api-$n-$$-$RANDOM"
    if ! docker service create --quiet --detach --name "$svc" \
        --network syslog_net \
        --constraint "node.labels.pg_id==$n" \
        --restart-condition none \
        "$CURL_IMAGE" "$@" -w '\n%{http_code}|%{errormsg}' \
        >/dev/null 2>&1; then
        echo "|000|docker service create failed (see: docker service create --constraint node.labels.pg_id==$n ...)"
        return
    fi

    local svc_state=""
    for _ in $(seq 1 30); do
        svc_state="$(docker service ps "$svc" --filter "desired-state=shutdown" --format '{{.CurrentState}}' 2>/dev/null | head -1)"
        [[ "$svc_state" == Complete* || "$svc_state" == Failed* ]] && break
        sleep 1
    done

    if [[ "$svc_state" != Complete* && "$svc_state" != Failed* ]]; then
        docker service rm "$svc" >/dev/null 2>&1 || true
        echo "|000|helper task never finished (last state: ${svc_state:-pending - no node matched node.labels.pg_id==$n?})"
        return
    fi

    local out
    out="$(docker service logs --raw "$svc" 2>&1 || true)"
    docker service rm "$svc" >/dev/null 2>&1 || true
    echo "$out"
}

patroni_get() {
    local n="$1"
    patroni_curl "$n" -s --max-time 5 "http://postgres$n:8008/"
}

patroni_post() {
    local n="$1" path="$2" body="$3"
    patroni_curl "$n" -s --max-time 10 \
        -XPOST -H 'Content-Type: application/json' -d "$body" \
        "http://postgres$n:8008/$path"
}

# Splits patroni_curl's output into body / http_code / errormsg.
parse_patroni_response() {
    local raw="$1"
    RESP_LAST_LINE="$(echo "$raw" | tail -1)"
    RESP_BODY="$(echo "$raw" | sed '$d')"
    RESP_CODE="${RESP_LAST_LINE%%|*}"
    RESP_ERR="${RESP_LAST_LINE#*|}"
}

# ---------------------------------------------------------------------------
# 1. Find the current leader across all three nodes, and refuse to touch
#    $TARGET if it turns out to be the leader itself.
# ---------------------------------------------------------------------------
echo "=== Locating current leader ==="
LEADER=""
TARGET_ROLE=""
for n in 1 2 3; do
    member="postgres$n"
    parse_patroni_response "$(patroni_get "$n")"
    if [[ "$RESP_CODE" != "200" ]]; then
        echo "  $member: unreachable (HTTP ${RESP_CODE:-000}${RESP_ERR:+: $RESP_ERR})"
        continue
    fi
    role="$(echo "$RESP_BODY" | jq -r '.role // "unknown"' 2>/dev/null || echo unknown)"
    state="$(echo "$RESP_BODY" | jq -r '.state // "unknown"' 2>/dev/null || echo unknown)"
    echo "  $member: role=$role state=$state"
    [[ "$member" == "$TARGET" ]] && TARGET_ROLE="$role"
    if [[ "$role" == "master" || "$role" == "primary" || "$role" == "leader" ]]; then
        LEADER="$member"
    fi
done

if [[ -z "$LEADER" ]]; then
    echo "ERROR: could not find a healthy leader among postgres1/2/3 - fix that first." >&2
    exit 1
fi

if [[ "$TARGET" == "$LEADER" ]]; then
    echo "ERROR: $TARGET is the current leader - refusing to reinitialize it." >&2
    echo "       If the leader itself is broken, you need a failover/switchover, not this script." >&2
    exit 1
fi

echo "Leader is $LEADER; recovering $TARGET (currently role=${TARGET_ROLE:-unknown})."

# ---------------------------------------------------------------------------
# 2. Confirm - this wipes $TARGET's local pgdata
# ---------------------------------------------------------------------------
if [[ "$CONFIRM" != "--yes" ]]; then
    echo
    echo "About to recover $TARGET:"
    echo "  - call POST /reinitialize (force) on $TARGET's Patroni REST API"
    echo "  - Patroni wipes $TARGET's local pgdata and re-clones a fresh"
    echo "    pg_basebackup from $LEADER"
    echo "  - wait for $TARGET to rejoin as a healthy streaming replica"
    echo
    read -r -p "Continue? [y/N] " reply
    [[ "$reply" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 1; }
fi

# ---------------------------------------------------------------------------
# 3. Trigger reinitialize. force=true because Patroni otherwise refuses if
#    it still considers postgres "running" - which it does here, it's just
#    stuck crash-looping on FATAL errors rather than actually down.
# ---------------------------------------------------------------------------
echo "=== Triggering reinitialize on $TARGET ==="
parse_patroni_response "$(patroni_post "$NODE" reinitialize '{"force": true}')"
echo "  HTTP ${RESP_CODE:-000}${RESP_ERR:+ ($RESP_ERR)}: ${RESP_BODY:-<empty>}"

if [[ "$RESP_CODE" != "200" ]]; then
    echo "ERROR: reinitialize request was not accepted - see response above." >&2
    if [[ "$RESP_CODE" == "000" ]]; then
        echo "       This node's Patroni REST API (postgres$NODE:8008) could not be reached" >&2
        echo "       even from a helper pinned to node.labels.pg_id==$NODE. Check directly:" >&2
        echo "         docker node ls --format '{{.Hostname}} {{.Status}} {{.ManagerStatus}}'" >&2
        echo "         docker node inspect --format '{{.Spec.Labels}}' <node>   # confirm pg_id=$NODE exists on exactly one node" >&2
        echo "         docker service ps $SERVICE --no-trunc                    # which node actually runs $TARGET" >&2
        echo "         docker exec \$(docker ps -qf name=$SERVICE) curl -sv http://localhost:8008/   # run ON that node" >&2
    fi
    exit 1
fi

# ---------------------------------------------------------------------------
# 4. Poll until $TARGET reports itself as a healthy running replica.
# ---------------------------------------------------------------------------
echo "=== Waiting for $TARGET to come back as a streaming replica ==="
RECOVERED=""
for i in $(seq 1 "$TIMEOUT_ITERS"); do
    parse_patroni_response "$(patroni_get "$NODE")"
    if [[ "$RESP_CODE" != "200" ]]; then
        echo "  ($i/$TIMEOUT_ITERS) unreachable (HTTP ${RESP_CODE:-000}${RESP_ERR:+: $RESP_ERR})"
        sleep 8
        continue
    fi
    role="$(echo "$RESP_BODY" | jq -r '.role // "unknown"' 2>/dev/null || echo unknown)"
    state="$(echo "$RESP_BODY" | jq -r '.state // "unknown"' 2>/dev/null || echo unknown)"
    echo "  ($i/$TIMEOUT_ITERS) role=$role state=$state"
    if [[ "$state" == "running" && ( "$role" == "replica" || "$role" == "sync_standby" ) ]]; then
        RECOVERED=1
        break
    fi
    sleep 8
done

if [[ -z "$RECOVERED" ]]; then
    echo "ERROR: $TARGET never reached role=replica/state=running after ~$((TIMEOUT_ITERS * 13))s." >&2
    echo "       Check what's stuck:" >&2
    echo "         docker service logs $SERVICE --tail 50" >&2
    echo "         docker service ps $SERVICE --no-trunc" >&2
    exit 1
fi

echo "$TARGET is back up as a streaming replica."

# ---------------------------------------------------------------------------
# 5. Verify from the leader's side - confirm it sees an active streaming
#    connection from $TARGET, not just that $TARGET thinks it's fine.
# ---------------------------------------------------------------------------
echo
echo "=== Verify from $LEADER ==="
echo "Run this to confirm the leader sees $TARGET streaming with low lag:"
echo
echo "  docker exec \$(docker ps -qf name=logmara-pg_$LEADER) sh -c \\"
echo "    'PGPASSWORD=\$(cat /run/secrets/pg_superuser_password) psql -U postgres -h localhost \\"
echo "     -c \"SELECT application_name, client_addr, state, sync_state, replay_lag FROM pg_stat_replication;\"'"
