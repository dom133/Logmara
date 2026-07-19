#!/bin/sh
if [ -s /data/ssl/server.crt ]; then
    wget --no-verbose --tries=1 --no-check-certificate --spider https://localhost:443/ || exit 1
else
    wget --no-verbose --tries=1 --spider http://localhost:80/ || exit 1
fi
