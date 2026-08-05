#!/bin/bash
# notify_vip.sh - called by keepalived on VRRP state transition.
# Creates the VIP marker file on the NFS volume when this node becomes MASTER,
# removes it when stepping down to BACKUP. The API tailer watches this file
# to determine whether it should run (only the VIP holder's API tails the
# log file, so rsyslog and tailer are always co-located on the same node,
# eliminating NFS read-cache delay and malformed JSON from split lines).
#
# Arguments (passed by keepalived):
#   $1 = script name (notify_vip)
#   $2 = event type (MASTER, BACKUP, FAULT)
#   $3 = vip list (not used here)
#   $4 = marker file path (rendered from keepalived.conf.tpl ${VIP_MARKER_PATH})

set -euo pipefail

EVENT="${2:-}"
MARKER="${4:-/data/.vip_master}"

case "$EVENT" in
  MASTER)
    # This node now holds the VIP - create marker so local API tailer starts
    touch "$MARKER"
    ;;
  BACKUP|FAULT)
    # Stepping down - remove marker so local API tailer stops
    rm -f "$MARKER"
    ;;
esac
