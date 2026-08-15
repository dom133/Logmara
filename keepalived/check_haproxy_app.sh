#!/bin/bash
# keepalived vrrp_script: only advertise/hold the VIP on this node while its
# local haproxy-app is actually accepting connections on :80. haproxy-app
# runs Swarm `mode: global` with `mode: host` port publishing (docker-
# stack.app.yml), so every edge=true node always has its own local instance
# on 127.0.0.1:80 - same reasoning as check_rsyslog.sh, just for the other
# service that now shares the VIP.
set -e

timeout 2 bash -c 'exec 3<>/dev/tcp/127.0.0.1/80' && exit 0
exit 1
