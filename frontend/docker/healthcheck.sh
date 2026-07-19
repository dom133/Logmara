#!/bin/sh
# https.conf is non-empty only while https_enabled is on (see
# handler.reloadNginx); a present-but-unused cert file isn't enough to know
# nginx is actually listening on 443.
if [ -s /data/nginx/https.conf ]; then
    wget --no-verbose --tries=1 --no-check-certificate --spider https://localhost:443/ || exit 1
else
    wget --no-verbose --tries=1 --spider http://localhost:80/ || exit 1
fi
