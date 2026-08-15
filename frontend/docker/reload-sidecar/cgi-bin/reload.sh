#!/bin/sh
echo "Content-Type: text/plain"
echo ""
OUT=$(nginx -s reload 2>&1)
if [ $? -eq 0 ]; then
    echo "reloaded"
else
    echo "reload failed: $OUT"
fi
