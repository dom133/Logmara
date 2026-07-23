#!/bin/sh
set -e

SSL_DIR=/data/ssl
NGINX_CONF_DIR=/data/nginx

mkdir -p "$SSL_DIR" "$NGINX_CONF_DIR"

if [ ! -f "$SSL_DIR/server.crt" ] || [ ! -f "$SSL_DIR/server.key" ]; then
    echo "No SSL certificate found, generating self-signed placeholder..."
    # Modern browsers ignore the CN entirely and require a matching Subject
    # Alternative Name - without one, every connection to this placeholder
    # cert fails validation, and not just the page-load interstitial that's
    # easy to click through: stricter paths like Service Worker script
    # fetches enforce it with no override, which surfaces as an opaque SSL
    # error with no indication the cert itself is the problem. Set
    # SSL_PLACEHOLDER_SAN to add the IP/hostname this is actually reached
    # on (e.g. "IP:10.1.10.20" or "DNS:syslog.example.com"). This is still
    # just a temporary self-signed cert either way - replace it with a real
    # one via Admin > Settings > SSL for anything beyond local testing.
    SAN="DNS:localhost,IP:127.0.0.1"
    if [ -n "$SSL_PLACEHOLDER_SAN" ]; then
        SAN="$SAN,$SSL_PLACEHOLDER_SAN"
    fi
    openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
        -keyout "$SSL_DIR/server.key" \
        -out "$SSL_DIR/server.crt" \
        -subj "/CN=localhost" \
        -addext "subjectAltName=$SAN" >/dev/null 2>&1
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

if [ ! -f "$NGINX_CONF_DIR/cors.conf" ]; then
    # Default-deny map so nginx has a valid $cors_allow_origin definition
    # before the backend's first sync writes the real allowed-origins list.
    cat > "$NGINX_CONF_DIR/cors.conf" <<'EOF'
map $http_origin $cors_allow_origin {
    default "";
}
EOF
fi

# httpd lives in the separate busybox-extras binary, not the base busybox
# (which only has the applets baked into Alpine's minimal `busybox` package).
busybox-extras httpd -f -p 8081 -h /srv/reload-sidecar &

exec /docker-entrypoint.sh nginx -g "daemon off;"
