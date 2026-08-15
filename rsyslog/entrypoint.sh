#!/bin/sh
set -e

RELAY_DIR=/data/relay

mkdir -p "$RELAY_DIR"

# The API (backend/relaypki) is the normal source of the relay CA/server
# cert, generated the first time an admin enables relay ingestion or issues
# a certificate. This container can start before that has ever happened
# (first `docker compose up`, or the feature simply never turned on) - the
# mTLS listener on 6514 still needs *some* valid cert to bind, so generate
# a placeholder pair here if missing. Whichever of the two (this script or
# the API's relaypki.EnsureCA) writes a file first "wins" - both write via
# a temp-file-then-rename so a concurrent reader never sees a torn file.
if [ ! -f "$RELAY_DIR/ca.crt" ] || [ ! -f "$RELAY_DIR/ca.key" ]; then
    echo "No relay CA found, generating placeholder..."
    umask 077
    openssl ecparam -name prime256v1 -genkey -noout -out "$RELAY_DIR/ca.key.tmp"
    openssl req -x509 -new -key "$RELAY_DIR/ca.key.tmp" -days 3650 \
        -out "$RELAY_DIR/ca.crt.tmp" -subj "/CN=Syslytics Relay CA" 2>/dev/null
    chmod 600 "$RELAY_DIR/ca.key.tmp"
    chmod 644 "$RELAY_DIR/ca.crt.tmp"
    if [ -f "$RELAY_DIR/ca.key" ]; then rm -f "$RELAY_DIR/ca.key.tmp"; else mv "$RELAY_DIR/ca.key.tmp" "$RELAY_DIR/ca.key"; fi
    if [ -f "$RELAY_DIR/ca.crt" ]; then rm -f "$RELAY_DIR/ca.crt.tmp"; else mv "$RELAY_DIR/ca.crt.tmp" "$RELAY_DIR/ca.crt"; fi
fi

# Always sign the server cert against whatever ca.key/ca.crt is on disk at
# this point (the block above already resolved which one that is) - never
# against a freshly generated CA, or a relay's client cert validation
# against the real ca.crt would fail with a mismatched chain.
if [ ! -f "$RELAY_DIR/server.crt" ] || [ ! -f "$RELAY_DIR/server.key" ]; then
    echo "No relay server certificate found, generating one signed by the current CA..."
    umask 077
    openssl ecparam -name prime256v1 -genkey -noout -out "$RELAY_DIR/server.key.tmp"
    openssl req -new -key "$RELAY_DIR/server.key.tmp" -out /tmp/relay-server.csr \
        -subj "/CN=syslog-relay-central" 2>/dev/null
    openssl x509 -req -in /tmp/relay-server.csr -CA "$RELAY_DIR/ca.crt" -CAkey "$RELAY_DIR/ca.key" \
        -CAcreateserial -CAserial /tmp/relay-ca.srl -days 3650 -out "$RELAY_DIR/server.crt.tmp" 2>/dev/null
    rm -f /tmp/relay-server.csr /tmp/relay-ca.srl
    chmod 600 "$RELAY_DIR/server.key.tmp"
    chmod 644 "$RELAY_DIR/server.crt.tmp"
    if [ -f "$RELAY_DIR/server.key" ]; then rm -f "$RELAY_DIR/server.key.tmp"; else mv "$RELAY_DIR/server.key.tmp" "$RELAY_DIR/server.key"; fi
    if [ -f "$RELAY_DIR/server.crt" ]; then rm -f "$RELAY_DIR/server.crt.tmp"; else mv "$RELAY_DIR/server.crt.tmp" "$RELAY_DIR/server.crt"; fi
fi

if [ ! -f "$RELAY_DIR/allowed-relays.conf" ]; then
    # Relay ingestion is disabled by default and the API hasn't necessarily
    # run yet - default to dropping every connection on the relay listener
    # until an admin enables the feature and whitelists at least one relay.
    printf '# No relay whitelist yet - dropping all connections.\nstop\n' > "$RELAY_DIR/allowed-relays.conf"
fi

# Debian's busybox build (unlike Alpine's, which splits httpd out into the
# separate busybox-extras package) includes the httpd applet directly.
# Backgrounded so rsyslogd can run in the foreground below and receive
# signals normally (docker stop, etc).
busybox httpd -f -p 8082 -h /srv/reload-sidecar &

exec rsyslogd -n
