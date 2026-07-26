#!/bin/bash
# keepalived notify_master hook: run on the node that just won the NFS VIP
# election. Brings up the DRBD device as Primary, mounts it, and starts
# exporting/serving NFS. Idempotent - keepalived may call this more than
# once (e.g. on a priority re-check) without a state change in between.
#
# Installed as /etc/keepalived/promote_nfs.sh - see nfs-ha/keepalived-
# nfs.conf.tpl and README "Optional: NFS Replica (DRBD + keepalived)".
set -euo pipefail

MOUNT_POINT="${NFS_HA_MOUNT_POINT:-/srv/syslog-ha/nfs}"

logger -t promote_nfs "promoting to primary"

drbdadm primary nfs-ha 2>/dev/null || true

# Wait for the promote to actually land (drbdadm primary can return before
# the role transition is fully visible to `drbdadm role`).
for _ in $(seq 1 15); do
    role=$(drbdadm role nfs-ha)
    [[ "$role" == Primary/* ]] && break
    sleep 1
done

mountpoint -q "$MOUNT_POINT" || mount /dev/drbd0 "$MOUNT_POINT"

exportfs -ra
systemctl start nfs-kernel-server

logger -t promote_nfs "primary, mounted, exporting"
