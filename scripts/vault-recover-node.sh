#!/usr/bin/env bash
#
# Recover a single Vault raft node that's stuck out of sync with the
# cluster - symptoms in `docker service logs logmara-vault_vault-N`:
#   - repeated "tls: internal error" while dialing the cluster port (8201)
#   - "core: forward request error" / "error during forwarded RPC request"
#   - "storage.raft: Election timeout reached, restarting election"
#   - "failed to get previous log: previous-index=X last-index=Y error=\"log not found\""
#     (the node has fallen far enough behind that normal AppendEntries
#     replication can't catch it up - it needs a full snapshot instead)
#
# This removes the node from the raft peer set, wipes its on-disk raft
# data, and restarts it so `retry_join` in vault.hcl makes it rejoin from
# scratch and pull a full snapshot from the current leader - instead of
# trying to patch a diverged log. It does NOT touch the other two nodes;
# your KV/dynamic-secrets data is safe as long as at least one of them
# stays up throughout (raft requires a quorum of 2/3 the whole time).
#
# Usage:
#   VAULT_TOKEN=<root-token> ./scripts/vault-recover-node.sh <1|2|3> [--yes]
#
#   <1|2|3>   Which vault-N node to recover
#   --yes     Skip the confirmation prompt (for non-interactive use)
#
# Needs, run from a Swarm manager with `docker` CLI + `jq` access to the cluster:
#   - VAULT_TOKEN: root token (see /srv/syslog-ha/vault-token on the host
#     that ran `vault-bootstrap.sh init`)
#   - Unseal keys: /tmp/vault_unseal_keys.txt (one per line, same file
#     `vault-bootstrap.sh unseal` reads) - or export VAULT_UNSEAL_KEYS as
#     comma-separated keys if that file isn't around anymore.
#
set -euo pipefail

NODE="${1:-}"
CONFIRM="${2:-}"

if [[ ! "$NODE" =~ ^[123]$ ]]; then
    echo "Usage: VAULT_TOKEN=<root-token> $0 <1|2|3> [--yes]" >&2
    exit 1
fi

if [[ -z "${VAULT_TOKEN:-}" ]]; then
    echo "ERROR: VAULT_TOKEN not set (root token)." >&2
    exit 1
fi

TARGET="vault-$NODE"
SERVICE="logmara-vault_vault-$NODE"
DATA_HOST_DIR="/srv/syslog-ha/vault/vault$NODE"

vault_cli() {
    local addr="$1"; shift
    docker run --rm -i \
        --network syslog_net \
        -e VAULT_ADDR="$addr" \
        -e VAULT_TOKEN="$VAULT_TOKEN" \
        hashicorp/vault:1.16.0 "$@"
}

# ---------------------------------------------------------------------------
# 1. Find a healthy peer to drive the recovery from (must not be $TARGET) -
#    remove-peer and list-peers both have to be issued against a node that's
#    actually part of a functioning quorum, not the broken one.
# ---------------------------------------------------------------------------
HEALTHY_ADDR=""
for n in 1 2 3; do
    [[ "$n" == "$NODE" ]] && continue
    addr="http://vault-$n:8200"
    if (docker run --rm --network syslog_net -e VAULT_ADDR="$addr" \
        hashicorp/vault:1.16.0 status 2>/dev/null || true) | grep -qi "^Sealed *false"; then
        HEALTHY_ADDR="$addr"
        echo "Using vault-$n ($addr) as the healthy peer to drive recovery of $TARGET."
        break
    fi
done

if [[ -z "$HEALTHY_ADDR" ]]; then
    echo "ERROR: neither other vault node is reachable and unsealed - can't safely recover $TARGET." >&2
    echo "       Fix whichever of the other two nodes is down first (raft needs 2/3 quorum)." >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# 2. Confirm - this wipes $TARGET's on-disk raft data
# ---------------------------------------------------------------------------
if [[ "$CONFIRM" != "--yes" ]]; then
    echo
    echo "About to recover $TARGET:"
    echo "  - remove it from the raft peer set (if currently listed)"
    echo "  - stop $SERVICE"
    echo "  - back up + wipe $DATA_HOST_DIR/data on the vault_id=$NODE host"
    echo "  - restart it so it rejoins fresh and gets a snapshot from the leader"
    echo "  - unseal it"
    echo
    read -r -p "Continue? [y/N] " reply
    [[ "$reply" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 1; }
fi

# ---------------------------------------------------------------------------
# 3. Remove from the raft peer set, if it's currently listed
# ---------------------------------------------------------------------------
echo "=== Checking raft peer list ==="
PEER_ID=$(vault_cli "$HEALTHY_ADDR" operator raft list-peers -format=json 2>/dev/null \
    | jq -r ".config.servers[] | select(.address==\"$TARGET:8201\") | .node_id" 2>/dev/null || true)

if [[ -n "$PEER_ID" ]]; then
    echo "Removing peer $PEER_ID ($TARGET) from raft..."
    vault_cli "$HEALTHY_ADDR" operator raft remove-peer "$PEER_ID"
else
    echo "$TARGET not currently listed as a peer - skipping remove-peer."
fi

# ---------------------------------------------------------------------------
# 4. Stop the node
# ---------------------------------------------------------------------------
echo "=== Stopping $SERVICE ==="
docker service scale "$SERVICE=0" >/dev/null

# ---------------------------------------------------------------------------
# 5. Back up + wipe its raft data. The data dir is a host bind mount that
#    only exists on whichever Swarm node carries the vault_id=$NODE label
#    (see docker-stack.vault.yml placement constraints) - a plain `docker
#    run` from wherever this script happens to execute isn't guaranteed to
#    land there, so pin a one-shot service to that node instead, the same
#    way vault-bootstrap.sh's read_docker_secret_value reaches an arbitrary
#    node to read a secret.
# ---------------------------------------------------------------------------
echo "=== Wiping raft data on the vault_id=$NODE host ==="
WIPE_SVC="vault-wipe-$NODE-$$"
docker service create --quiet --detach --name "$WIPE_SVC" \
    --constraint "node.labels.vault_id==$NODE" \
    --restart-condition none \
    --mount "type=bind,source=$DATA_HOST_DIR,target=/vaultdir" \
    alpine sh -c "mv /vaultdir/data /vaultdir/data.bak-\$(date +%Y%m%d-%H%M%S) 2>/dev/null; mkdir -p /vaultdir/data && chmod -R 777 /vaultdir/data && echo WIPE_DONE" \
    >/dev/null

echo "Waiting for wipe to finish..."
for _ in $(seq 1 30); do
    docker service logs "$WIPE_SVC" 2>/dev/null | grep -q WIPE_DONE && break
    sleep 1
done
docker service logs "$WIPE_SVC" 2>&1 || true
docker service rm "$WIPE_SVC" >/dev/null 2>&1 || true
echo "Data wiped (previous data backed up as $DATA_HOST_DIR/data.bak-* alongside it)."

# ---------------------------------------------------------------------------
# 6. Restart - retry_join in vault.hcl makes it rejoin and pull a fresh
#    snapshot from the leader instead of patching a diverged log
# ---------------------------------------------------------------------------
echo "=== Restarting $SERVICE ==="
docker service scale "$SERVICE=1" >/dev/null

echo "Waiting for $TARGET to respond..."
for i in $(seq 1 30); do
    if (docker run --rm --network syslog_net -e VAULT_ADDR="http://$TARGET:8200" \
        hashicorp/vault:1.16.0 status 2>/dev/null || true) | grep -qi "sealed"; then
        echo "$TARGET is responding."
        break
    fi
    echo "Waiting... ($i/30)"
    sleep 2
done

# ---------------------------------------------------------------------------
# 7. Unseal
# ---------------------------------------------------------------------------
echo "=== Unsealing $TARGET ==="
if [[ -n "${VAULT_UNSEAL_KEYS:-}" ]]; then
    IFS=',' read -r -a KEYS <<< "$VAULT_UNSEAL_KEYS"
elif [[ -f /tmp/vault_unseal_keys.txt ]]; then
    mapfile -t KEYS < /tmp/vault_unseal_keys.txt
else
    echo "ERROR: no unseal keys found (set VAULT_UNSEAL_KEYS or provide /tmp/vault_unseal_keys.txt)." >&2
    exit 1
fi

for i in 0 1 2; do
    docker run --rm --network syslog_net -e VAULT_ADDR="http://$TARGET:8200" \
        hashicorp/vault:1.16.0 operator unseal "${KEYS[$i]}" >/dev/null 2>&1 || true
done

# ---------------------------------------------------------------------------
# 8. Verify
# ---------------------------------------------------------------------------
echo "=== Verifying ==="
sleep 3
vault_cli "$HEALTHY_ADDR" operator raft list-peers
echo
echo "Confirm $TARGET shows up above as a voter, and that its Raft Applied"
echo "Index (vault status on $TARGET) matches the leader's before trusting it."
