#!/bin/sh
# Clear the selection even when the child process crashes.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
binary="$root/bin/mihomo-capture"
config="${1:-$root/config.yaml}"
child=""
clear() { "$binary" -config "$config" -clear-gateway; }
stop() {
    trap '' TERM INT HUP
    if [ -n "$child" ]; then
        kill -TERM "$child" 2>/dev/null || true
        wait "$child" 2>/dev/null || true
    fi
    clear || true
    exit 0
}
trap stop TERM INT HUP
"$binary" -config "$config" &
child=$!
status=0
wait "$child" || status=$?
child=""
clear || true
exit "$status"
