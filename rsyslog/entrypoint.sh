#!/bin/sh
set -e

RELAY_DIR=/data/relay

mkdir -p "$RELAY_DIR"

# The api service is the normal source of the relay CA/server cert
# (handler.SyncRelayConfig calls relaypki.EnsureCA at its own startup and
# hourly thereafter), but this container can start before that has ever
# happened (first `docker compose up`, or the feature simply never turned
# on) - the mTLS listener on 6514 still needs *some* valid cert to bind.
# relaybootstrap is a tiny CLI wrapped around that exact same EnsureCA
# function (see backend/cmd/relaybootstrap) - not a separate reimplementation
# - so there is exactly one piece of code that knows how to generate/renew
# this CA and server certificate, however many processes end up calling it.
# It's idempotent (only creates what's missing, renews what's expiring) and
# safe to race against the api service's own call to it: both write via a
# temp-file-then-rename, so a concurrent reader never sees a torn file, and
# whichever gets there first for a brand-new deployment wins - either way
# it's the same code, so the result is identical (RSA 4096).
relaybootstrap "$RELAY_DIR"

if [ ! -f "$RELAY_DIR/allowed-relays.conf" ]; then
    # Relay ingestion is disabled by default and the API hasn't necessarily
    # run yet - bind the mTLS listener but reject every connection (fail
    # closed) until an admin enables the feature and whitelists at least
    # one relay. handler.writeRelayACL overwrites this with the real
    # ruleset + input() + PermittedPeer list once the API has run - see
    # its doc comment for why the listener itself, not just the IP
    # allow-list, has to be regenerated dynamically.
    cat > "$RELAY_DIR/allowed-relays.conf" <<'EOF'
# No relay whitelist yet - dropping all connections.
ruleset(name="relayIngest") {
    stop
}
input(type="imtcp" port="6514" ruleset="relayIngest"
  StreamDriver.Name="gtls"
  StreamDriver.Mode="1"
  StreamDriver.AuthMode="x509/name"
  PermittedPeer=["no-relay-certificates-active"]
)
EOF
fi

# Debian's busybox build (unlike Alpine's, which splits httpd out into the
# separate busybox-extras package) includes the httpd applet directly.
busybox httpd -f -p 8082 -h /srv/reload-sidecar &

# rsyslogd has no true hot-reload: SIGHUP only reopens output files as of
# rsyslog 4.5.1+ ($HUPisRestart, which made HUP do a full restart, defaults
# to off and is deprecated/removed upstream) - it never rereads
# rsyslog.conf or anything it include()s, so config changes (like the
# relay ACL above) would otherwise never take effect after this first
# parse. Run rsyslogd as a supervised child instead of as PID 1 so
# reload-sidecar/cgi-bin/reload.sh can force a real restart - the only way
# rsyslog actually picks up a new config - by killing just this child,
# without taking the whole container (and this httpd sidecar) down with
# it. Every such restart briefly drops connections on BOTH port 514 and
# 6514, since one process serves both - by design, not a bug: it's the
# same cost any rsyslog config change pays outside Docker too.
#
# set -e (above) would otherwise abort this whole script - and so kill the
# container - the moment rsyslogd exits for any reason, since a killed or
# crashed child makes `wait` return non-zero; `|| true` on it keeps the
# loop itself immune to that so it can respawn instead.
trap 'kill -TERM "$RSYSLOG_PID" 2>/dev/null; wait "$RSYSLOG_PID" 2>/dev/null; exit 0' TERM INT

while true; do
    start=$(date +%s)
    rsyslogd -n &
    RSYSLOG_PID=$!
    wait "$RSYSLOG_PID" || true
    # Guards against a hot crash-loop (e.g. a bad generated config) pegging
    # the CPU - only kicks in when rsyslogd exited almost immediately.
    elapsed=$(( $(date +%s) - start ))
    if [ "$elapsed" -lt 2 ]; then
        sleep 2
    fi
done
