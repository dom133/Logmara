#!/bin/bash
# keepalived vrrp_script: only let this node hold/keep the NFS VIP while its
# DRBD replica is UpToDate (not still resyncing/disconnected/behind) AND
# nfsd is actually listening locally. Failing this makes keepalived release
# the VIP so the peer (if it's healthy) can take over - see
# nfs-ha/keepalived-nfs.conf.tpl.
set -e

ds=$(drbdadm dstate nfs-ha)
[[ "$ds" == "UpToDate/UpToDate" ]] || exit 1

timeout 2 bash -c 'exec 3<>/dev/tcp/127.0.0.1/2049' && exit 0
exit 1
