# DRBD resource template backing the optional synchronous NFS replica
# (nfs1/nfs2 - see README "Optional: NFS Replica (DRBD + keepalived)").
# Protocol C = synchronous replication: a write only acknowledges once both
# nodes have it on disk, so failover loses nothing already ack'd to the app
# tier - the same zero-data-loss bar as Patroni's synchronous_mode option,
# just for a filesystem instead of Postgres.
#
# Render one identical copy of this file to /etc/drbd.d/nfs-ha.res on BOTH
# nfs1 and nfs2 (it's symmetric - unlike keepalived.conf.tpl there's no
# per-node STATE/PRIORITY split here), substituting:
#   ${NFS1_HOST}   nfs1's hostname (must match `hostname` output on that box, or add matching `on` sections keyed by hostname)
#   ${NFS1_IP}     nfs1's real interface IP
#   ${NFS2_HOST}   nfs2's hostname
#   ${NFS2_IP}     nfs2's real interface IP
#   ${DRBD_DISK}   the backing block device on both nodes, e.g. /dev/vg0/nfsdata
#                  (must be an unformatted/dedicated volume of identical size
#                  on both nodes - DRBD owns it, mkfs happens on /dev/drbd0
#                  afterwards, never directly on ${DRBD_DISK})
#   ${DRBD_SECRET} shared HMAC secret authenticating the replication link,
#                  e.g. `openssl rand -hex 16` - same value on both nodes
#
# Example (envsubst < nfs-ha/drbd-nfs.res.tpl):
#   export NFS1_HOST=nfs1 NFS1_IP=10.0.0.30 NFS2_HOST=nfs2 NFS2_IP=10.0.0.31 \
#          DRBD_DISK=/dev/vg0/nfsdata DRBD_SECRET=$(openssl rand -hex 16)
#   envsubst < nfs-ha/drbd-nfs.res.tpl | sudo tee /etc/drbd.d/nfs-ha.res

resource nfs-ha {
    protocol C;

    disk {
        resync-rate 60M;
        # Keeps the peer's copy from silently drifting after a bitmap-based
        # resync races with a fresh write, at the cost of pausing new I/O
        # during resync - acceptable here since resync only happens right
        # after a promote/demote or reconnect, not during steady-state.
        c-plan-ahead 0;
    }

    net {
        cram-hmac-alg sha1;
        shared-secret "${DRBD_SECRET}";
    }

    on ${NFS1_HOST} {
        device /dev/drbd0;
        disk ${DRBD_DISK};
        address ${NFS1_IP}:7789;
        meta-disk internal;
    }

    on ${NFS2_HOST} {
        device /dev/drbd0;
        disk ${DRBD_DISK};
        address ${NFS2_IP}:7789;
        meta-disk internal;
    }
}
