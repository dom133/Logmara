#!/bin/bash
# notify_vip.sh - called by keepalived on VRRP state transition.
# Writes this node's hostname into the VIP marker file on the NFS volume
# when this node becomes MASTER. The API tailer reads the marker and only
# starts if the stored hostname matches its own MY_NODE env var (set from
# Swarm's {{.Node.Hostname}} template var). This guarantees that only the
# replica on the VIP-holding node tails the log.
#
# Arguments (passed by keepalived):
#   $1 = script name (notify_vip)
#   $2 = event type (MASTER, BACKUP, FAULT)
#   $3 = vip list (not used here)
#   $4 = marker file path (rendered from keepalived.conf.tpl)

set -euo pipefail

EVENT="${2:-}"
MARKER="${3:-/data/.vip_master}"

case "$EVENT" in
  MASTER)
    hostname > "$MARKER"
    ;;
esac
