#!/bin/sh
# 127.0.0.1, not localhost: nginx's "listen 80"/"listen 443 ssl" (see
# backend/handler/admin.go) bind IPv4 only, but wget resolves "localhost" to
# the IPv6 loopback (::1) first when it's present in /etc/hosts - which it
# is by default - and gets a connection-refused there before ever trying
# IPv4, failing the healthcheck even though nginx is up and reachable.
#
# https.conf is non-empty only while https_enabled is on (see
# handler.reloadNginx); a present-but-unused cert file isn't enough to know
# nginx is actually listening on 443.
if [ -s /data/nginx/https.conf ]; then
    wget --no-verbose --tries=1 --no-check-certificate --spider https://127.0.0.1:443/ || exit 1
else
    wget --no-verbose --tries=1 --spider http://127.0.0.1:80/ || exit 1
fi
