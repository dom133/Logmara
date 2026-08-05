#!/bin/bash
# notify_vip.sh - called by keepalived on VRRP state transition.
# Creates the VIP marker file on the NFS volume when this node becomes MASTER,
# removes it when stepping down to BACKUP. The API tailer watches this file
# to determine whether it should run (only the VIP holder's API tails the
# log file, so rsyslog and tailer are always co-located on the same node,
# eliminating NFS read-cache delay and malformed JSON from split lines).
#
# A local state file tracks whether this node has ever been MASTER. This
# prevents a BACKUP node from deleting the marker on startup (keepalived
# calls notify_backup on every node that enters BACKUP state, including
# the node that was always a BACKUP). Only the node that actually loses
# the VIP removes the marker.
#
# Arguments (passed by keepalived):
#   $1 = script name (notify_vip)
#   $2 = event type (MASTER, BACKUP, FAULT)
#   $3 = vip list (not used here)
#   $4 = marker file path (rendered from keepalived.conf.tpl)

set -euo pipefail

EVENT="${2:-}"
MARKER="${3:-/data/.vip_master}"
LOCAL_STATE="/var/run/keepalived-vip-master"

case "$EVENT" in
  MASTER)
    touch "$LOCAL_STATE"
    touch "$MARKER"
    ;;
  BACKUP|FAULT)
    # Only remove the marker if this node was previously MASTER.
    # A node that has always been BACKUP should not delete the marker
    # created by the actual MASTER node.
    if [ -f "$LOCAL_STATE" ]; then
      rm -f "$LOCAL_STATE" "$MARKER"
    fi
    ;;
esac
