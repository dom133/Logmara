#!/bin/bash
# keepalived notify_backup/notify_fault hook: run on a node that just lost
# (or never had) the NFS VIP. Stops serving NFS and steps the DRBD device
# down to Secondary, so it can safely resync from whichever node is Primary
# without a stale exporter still holding the filesystem open. Idempotent,
# same reasoning as promote_nfs.sh.
#
# Installed as /etc/keepalived/demote_nfs.sh - see nfs-ha/keepalived-
# nfs.conf.tpl and README "Optional: NFS Replica (DRBD + keepalived)".
set -euo pipefail

MOUNT_POINT="${NFS_HA_MOUNT_POINT:-/srv/syslog-ha/nfs}"

logger -t demote_nfs "demoting to secondary"

systemctl stop nfs-kernel-server || true
exportfs -ua || true

if mountpoint -q "$MOUNT_POINT"; then
    umount "$MOUNT_POINT"
fi

drbdadm secondary nfs-ha 2>/dev/null || true

logger -t demote_nfs "secondary, unmounted, not exporting"
