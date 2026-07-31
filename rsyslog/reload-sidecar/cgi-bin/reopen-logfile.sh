#!/bin/sh
# SIGHUP does NOT reread rsyslog.conf (see entrypoint.sh - that needs
# reload.sh's SIGTERM instead), but it does make rsyslogd close and reopen
# every currently-open output file, without restarting the process or
# dropping the 514/6514/6515 listeners. That's exactly what's needed after
# the api tailer atomically replaces /data/logs.jsonl via rename during log
# compaction (see backend/tailer/tailer.go's compactFile): rename swaps the
# directory entry, but rsyslogd's own long-lived omfile handle still points
# at the old, now-unlinked inode and would silently keep appending into it
# forever - invisible and unrecoverable - until told to reopen the path.
echo "Content-Type: text/plain"
echo ""
OUT=$(kill -HUP $(pgrep -x rsyslogd) 2>&1)
if [ $? -eq 0 ]; then
    echo "reopened"
else
    echo "reopen failed: $OUT"
fi
