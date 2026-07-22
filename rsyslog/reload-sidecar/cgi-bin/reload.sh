#!/bin/sh
# SIGHUP doesn't reread rsyslog.conf (see entrypoint.sh) - send SIGTERM to
# just the rsyslogd child instead, which entrypoint.sh's supervisor loop
# (running as PID 1, so never matched by `pgrep -x rsyslogd`) immediately
# restarts against whatever config is on disk now.
echo "Content-Type: text/plain"
echo ""
OUT=$(kill -TERM $(pgrep -x rsyslogd) 2>&1)
if [ $? -eq 0 ]; then
    echo "reloaded"
else
    echo "reload failed: $OUT"
fi
