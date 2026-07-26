# keepalived (VRRP) template for the optional NFS replica's floating VIP
# (nfs1/nfs2 - see README "Optional: NFS Replica (DRBD + keepalived)").
#
# Distinct from keepalived/keepalived.conf.tpl (the app/edge VIP fronting
# rsyslog + haproxy-app) - different nodes, different virtual_router_id (52
# vs 51, so the two VRRP instances never collide if they ever end up
# broadcasting on the same L2/L3 path), and this one drives DRBD
# promote/demote via notify_master/notify_backup instead of just gating an
# already-running service.
#
# Runs on the HOST on both nfs1 and nfs2 (VRRP needs direct L2 access, same
# reasoning as the app-tier keepalived). Render one file per node,
# substituting:
#   ${STATE}      MASTER on the node that should start as NFS primary, BACKUP on the other
#   ${PRIORITY}   higher wins election, e.g. 150 for MASTER, 100 for BACKUP
#   ${MY_IP}      this node's real interface IP (unicast_src_ip)
#   ${PEER_IP}    the other NFS node's real interface IP
#   ${VIP}        the floating IP docker-stack.app.yml's NFS_SERVER points at
#   ${VIP_CIDR}   e.g. 24
#   ${INTERFACE}  e.g. eth0
#   ${VRRP_AUTH_PASS}  shared 8-char VRRP auth password (same on both nodes)
#
# Unicast (not multicast) VRRP, same reasoning as the app-tier template.

global_defs {
    # See the matching comment in keepalived/keepalived.conf.tpl - required
    # for notify_master/notify_backup/track_script below to actually run
    # instead of silently failing closed.
    enable_script_security
}

vrrp_script check_nfs_drbd {
    script "/etc/keepalived/check_nfs_drbd.sh"
    interval 2
    timeout 2
    fall 2
    rise 2
    user root
}

vrrp_instance VI_NFS {
    state ${STATE}
    interface ${INTERFACE}
    virtual_router_id 52
    priority ${PRIORITY}
    advert_int 1

    unicast_src_ip ${MY_IP}
    unicast_peer {
        ${PEER_IP}
    }

    authentication {
        auth_type PASS
        auth_pass ${VRRP_AUTH_PASS}
    }

    virtual_ipaddress {
        ${VIP}/${VIP_CIDR} dev ${INTERFACE}
    }

    track_script {
        check_nfs_drbd
    }

    # Drives the actual DRBD promote/demote + mount/export/nfsd lifecycle -
    # this is what makes the VIP transition also mean something at the
    # filesystem level, unlike the app-tier VIP which only ever gates
    # already-running services.
    notify_master "/etc/keepalived/promote_nfs.sh"
    notify_backup "/etc/keepalived/demote_nfs.sh"
    notify_fault  "/etc/keepalived/demote_nfs.sh"
}
