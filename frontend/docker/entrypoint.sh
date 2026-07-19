#!/bin/sh
set -e

SSL_DIR=/data/ssl
NGINX_CONF_DIR=/data/nginx

mkdir -p "$SSL_DIR" "$NGINX_CONF_DIR"

if [ ! -f "$SSL_DIR/server.crt" ] || [ ! -f "$SSL_DIR/server.key" ]; then
    echo "No SSL certificate found, generating self-signed placeholder..."
    openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
        -keyout "$SSL_DIR/server.key" \
        -out "$SSL_DIR/server.crt" \
        -subj "/CN=localhost" >/dev/null 2>&1
    chmod 644 "$SSL_DIR/server.crt"
    chmod 600 "$SSL_DIR/server.key"
fi

if [ ! -f "$NGINX_CONF_DIR/redirect.conf" ]; then
    touch "$NGINX_CONF_DIR/redirect.conf"
fi

if [ ! -f "$NGINX_CONF_DIR/https.conf" ]; then
    # HTTPS is disabled by default; the backend writes the real 443 server
    # block here once https_enabled is turned on (via settings or env).
    touch "$NGINX_CONF_DIR/https.conf"
fi

# httpd lives in the separate busybox-extras binary, not the base busybox
# (which only has the applets baked into Alpine's minimal `busybox` package).
busybox-extras httpd -f -p 8081 -h /srv/reload-sidecar &

exec /docker-entrypoint.sh nginx -g "daemon off;"
