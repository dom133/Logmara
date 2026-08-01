#!/bin/sh
# 127.0.0.1, not localhost: nginx's "listen 80" (see frontend/nginx.conf)
# binds IPv4 only, but wget resolves "localhost" to the IPv6 loopback (::1)
# first when it's present in /etc/hosts - which it is by default - and gets
# a connection-refused there before ever trying IPv4, failing the healthcheck
# even though nginx is up and reachable.
#
# Always check :80 regardless of https_enabled. The :443 listener in Swarm
# expects a PROXY protocol header from haproxy-app (NGINX_PROXY_PROTOCOL=true
# in docker-stack.app.yml), but a local 127.0.0.1 connection has no PROXY
# header, so nginx would reject it with "broken header" errors. :80 works
# in both topologies (plain compose and Swarm) and is sufficient to confirm
# nginx is running and serving.
# Accept any HTTP response from nginx (200, 301, etc.). We only need to
# confirm nginx is running and responding, not what status it returns.
# BusyBox wget does NOT follow redirects by default, so a 301 from
# https_redirect won't cause it to hit :443 (which would fail with PROXY
# protocol errors in Swarm). -S dumps the server response headers to stderr,
# which is what we grep for.
wget -q --spider -S http://127.0.0.1:80/ -T 2 2>&1 | grep -q "HTTP/"
