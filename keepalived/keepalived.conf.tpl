# keepalived (VRRP) template for the syslog ingress VIP.
#
# Runs on the HOST (or a --network host, --cap-add=NET_ADMIN --cap-add=
# NET_BROADCAST container using the official `osixia/keepalived` image) on
# each node labeled edge=true — NOT as a Swarm service, since VRRP needs
# direct L2 access that Swarm's overlay network does not provide.
#
# Render one file per edge node from this template, substituting:
#   ${STATE}            MASTER on exactly one edge node, BACKUP on the rest
#   ${PRIORITY}         higher wins election, e.g. 150 for the MASTER, 100/90/... for BACKUPs
#   ${MY_IP}            this node's real interface IP (unicast_src_ip)
#   ${PEER_IPS}         the other edge nodes' real interface IPs, one per line
#   ${VIP}              the floating IP syslog senders / clients target
#   ${VIP_CIDR}         e.g. 24
#   ${INTERFACE}        e.g. eth0
#   ${VIP_MARKER_PATH}  full path to the NFS-mounted log_data directory on
#                       this host (e.g. /srv/syslog-ha/nfs/log_data)
#
# Unicast (not multicast) VRRP is used deliberately — it works across plain
# L3 routed networks and most cloud VPCs where multicast is blocked.

global_defs {
}

vrrp_script check_rsyslog {
    script "/etc/keepalived/check_rsyslog.sh"
    interval 2
    timeout 2
    fall 2
    rise 2
    user root
}

# Gates the VIP on haproxy-app (docker-stack.app.yml) too, now that it
# shares this VIP with rsyslog for load-balanced HTTP/API access - without
# this, the VIP could sit on a node where rsyslog is healthy but
# haproxy-app died, silently dropping all frontend/api traffic.
vrrp_script check_haproxy_app {
    script "/etc/keepalived/check_haproxy_app.sh"
    interval 2
    timeout 2
    fall 2
    rise 2
    user root
}

vrrp_instance VI_SYSLOG {
    state ${STATE}
    interface ${INTERFACE}
    virtual_router_id 51
    priority ${PRIORITY}
    advert_int 1

    unicast_src_ip ${MY_IP}
    unicast_peer {
${PEER_IPS}
    }

    authentication {
        auth_type PASS
        auth_pass ${VRRP_AUTH_PASS}
    }

    virtual_ipaddress {
        ${VIP}/${VIP_CIDR} dev ${INTERFACE}
    }

    track_script {
        check_rsyslog
        check_haproxy_app
    }

    notify_master "/etc/keepalived/notify_vip.sh notify_vip MASTER ${VIP_MARKER_PATH}/.vip_master"
    notify_backup "/etc/keepalived/notify_vip.sh notify_vip BACKUP ${VIP_MARKER_PATH}/.vip_master"
    notify_fault  "/etc/keepalived/notify_vip.sh notify_vip FAULT ${VIP_MARKER_PATH}/.vip_master"
}
