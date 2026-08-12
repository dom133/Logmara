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
# cluster (containers reach each other by service name over the
# `syslog_net` overlay network regardless of which node this script runs
# from - same as vault-recover-node.sh's vault_cli helper).
#
set -euo pipefail

NODE="${1:-}"
CONFIRM="${2:-}"
CURL_IMAGE="curlimages/curl:8.10.1"
TIMEOUT_ITERS=60   # 60 * 10s = 10 minutes, generous for a full pg_basebackup

if [[ ! "$NODE" =~ ^[123]$ ]]; then
    echo "Usage: $0 <1|2|3> [--yes]" >&2
    exit 1
fi

TARGET="postgres$NODE"
SERVICE="logmara-pg_postgres$NODE"

# ---------------------------------------------------------------------------
# Patroni REST API helper - runs a throwaway container on syslog_net so it
# works no matter which Swarm node this script executes from.
# ---------------------------------------------------------------------------
patroni_get() {
    local member="$1"
    docker run --rm --network syslog_net "$CURL_IMAGE" \
        -s --max-time 5 "http://$member:8008/" 2>/dev/null || true
}

patroni_post() {
    local member="$1" path="$2" body="$3"
    docker run --rm --network syslog_net "$CURL_IMAGE" \
        -s --max-time 10 -w '\n%{http_code}' \
        -XPOST -H 'Content-Type: application/json' -d "$body" \
        "http://$member:8008/$path" 2>/dev/null || true
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
    resp="$(patroni_get "$member")"
    [[ -z "$resp" ]] && { echo "  $member: unreachable"; continue; }
    role="$(echo "$resp" | jq -r '.role // "unknown"' 2>/dev/null || echo unknown)"
    state="$(echo "$resp" | jq -r '.state // "unknown"' 2>/dev/null || echo unknown)"
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
result="$(patroni_post "$TARGET" reinitialize '{"force": true}')"
http_code="$(echo "$result" | tail -1)"
body="$(echo "$result" | sed '$d')"
echo "  HTTP $http_code: ${body:-<empty>}"

if [[ "$http_code" != "200" ]]; then
    echo "ERROR: reinitialize request was not accepted - see response above." >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# 4. Poll until $TARGET reports itself as a healthy running replica.
# ---------------------------------------------------------------------------
echo "=== Waiting for $TARGET to come back as a streaming replica ==="
RECOVERED=""
for i in $(seq 1 "$TIMEOUT_ITERS"); do
    resp="$(patroni_get "$TARGET")"
    role="$(echo "$resp" | jq -r '.role // "unknown"' 2>/dev/null || echo unknown)"
    state="$(echo "$resp" | jq -r '.state // "unknown"' 2>/dev/null || echo unknown)"
    echo "  ($i/$TIMEOUT_ITERS) role=$role state=$state"
    if [[ "$state" == "running" && ( "$role" == "replica" || "$role" == "sync_standby" ) ]]; then
        RECOVERED=1
        break
    fi
    sleep 10
done

if [[ -z "$RECOVERED" ]]; then
    echo "ERROR: $TARGET never reached role=replica/state=running after $((TIMEOUT_ITERS * 10))s." >&2
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
