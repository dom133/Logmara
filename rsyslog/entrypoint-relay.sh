#!/bin/sh
set -e

TLS_DIR=/etc/syslog-relay/tls-local
mkdir -p "$TLS_DIR"

# Self-signed and unrelated to the ca.crt/client.crt/client.key in this
# relay's downloaded certificate bundle (those authenticate this relay TO
# the central server's mTLS listener - see relay.conf's global() block).
# This cert only exists so the relay's own local direct-TLS listener on
# 6514 (declared inside the downloaded relay.conf itself - see
# backend/handler/relay.go's relayConfSnippet) has something to present -
# that listener uses StreamDriver.AuthMode="anon", so nothing ever
# validates this certificate's chain; it's here purely because gtls
# requires a cert+key pair to be configured before it will accept TLS
# connections at all. Persisted under $TLS_DIR (see
# docker-compose.relay.yml's relay_tls volume) so it stays stable across
# restarts instead of forcing every device pinned to a specific
# fingerprint to re-trust a new one.
if [ ! -f "$TLS_DIR/server.crt" ] || [ ! -f "$TLS_DIR/server.key" ]; then
    openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
        -keyout "$TLS_DIR/server.key" -out "$TLS_DIR/server.crt" \
        -subj "/CN=syslog-relay" >/dev/null 2>&1
fi

exec rsyslogd -n
