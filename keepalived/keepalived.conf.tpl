# keepalived (VRRP) template for the syslog ingress VIP.
#
# Runs on the HOST (or a --network host, --cap-add=NET_ADMIN --cap-add=
# NET_BROADCAST container using the official `osixia/keepalived` image) on
# each node labeled edge=true — NOT as a Swarm service, since VRRP needs
# direct L2 access that Swarm's overlay network does not provide.
#
# Render one file per edge node from this template, substituting:
#   ${STATE}      MASTER on exactly one edge node, BACKUP on the rest
#   ${PRIORITY}   higher wins election, e.g. 150 for the MASTER, 100/90/... for BACKUPs
#   ${MY_IP}      this node's real interface IP (unicast_src_ip)
#   ${PEER_IPS}   the other edge nodes' real interface IPs, one per line
#   ${VIP}        the floating IP syslog senders / clients target
#   ${VIP_CIDR}   e.g. 24
#   ${INTERFACE}  e.g. eth0
#
# Unicast (not multicast) VRRP is used deliberately — it works across plain
# L3 routed networks and most cloud VPCs where multicast is blocked.

global_defs {
    # Required since keepalived 2.x: without this, keepalived refuses to
    # actually run vrrp_script below (it logs "SECURITY VIOLATION - scripts
    # are being executed but script_security not enabled" and synthesizes a
    # failing exit code instead) - which silently traps this node in FAULT
    # state forever, so it can never take over the VIP even when the peer
    # genuinely goes down. The keyword really is `enable_script_security`,
    # not the more-guessable `script_security` - the latter is silently
    # ignored as an unrecognized global_defs key (no parse error, no
    # effect) rather than rejected, which is what makes this so easy to
    # get wrong: see https://github.com/acassen/keepalived/issues/901.
    # `user root` on the script below is what makes it safe to enable
    # without also having to provision a dedicated unprivileged
    # 'keepalived_script' system user.
    enable_script_security
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

    # Optional: notify_master/notify_backup scripts can be added here to log
    # failover events or push a metric/alert.
}
