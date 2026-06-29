#!/bin/sh
set -eu

caddy run --config /etc/caddy/Caddyfile --adapter caddyfile &
cy=$!
proxywi-server &
pw=$!

term() { kill -TERM "$cy" "$pw" 2>/dev/null || true; }
trap term TERM INT

while kill -0 "$cy" 2>/dev/null && kill -0 "$pw" 2>/dev/null; do
	sleep 2
done

term
wait || true
exit 1
