#!/bin/bash
# notify_vip.sh - called by keepalived on VRRP state transition.
# Creates /data/.vip_master on the NFS volume when this node becomes MASTER,
# removes it when stepping down to BACKUP. The API tailer watches this file
# to determine whether it should run (only the VIP holder's API tails the
# log file, so rsyslog and tailer are always co-located on the same node,
# eliminating NFS read-cache delay and malformed JSON from split lines).
#
# Arguments (passed by keepalived):
#   $1 = script name (notify_vip)
#   $2 = event type (MASTER, BACKUP, FAULT)
#   $3 = vip list (not used here)

set -euo pipefail

EVENT="${2:-}"
NFS_PATH="${NFS_LOG_DATA_PATH:-/srv/syslog-ha/nfs/log_data}"
MARKER="${NFS_PATH}/.vip_master"

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
