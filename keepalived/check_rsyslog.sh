#!/bin/bash
# keepalived vrrp_script: only advertise/hold the VIP on this node while its
# local rsyslog is actually accepting connections. rsyslog runs Swarm
# `mode: global` with `mode: host` port publishing (docker-stack.app.yml),
# so every edge=true node always has its own local rsyslog on 127.0.0.1:514
# — this just confirms it's alive, it does not talk to Swarm/Patroni.
set -e

timeout 2 bash -c 'exec 3<>/dev/tcp/127.0.0.1/514' && exit 0
exit 1
