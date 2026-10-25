#!/usr/bin/env bash
# Start the built binary on a free loopback port, check key requests, stop it.
set -euo pipefail
# Loopback requests must not go through a configured HTTP(S)_PROXY.
export NO_PROXY="127.0.0.1,localhost${NO_PROXY:+,$NO_PROXY}" no_proxy="127.0.0.1,localhost${no_proxy:+,$no_proxy}"
bin=${1:?usage: backend-smoke.sh <binary>}
port=$(python3 -I -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
log=$(mktemp)
RADAR_ADMIN_TOKEN=smoke-token "$bin" api --store=mock --addr="127.0.0.1:$port" >"$log" 2>&1 &
pid=$!
cleanup() { kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; rm -f "$log"; }
trap cleanup EXIT
base="http://127.0.0.1:$port"
for _ in $(seq 1 50); do curl -fsS "$base/healthz" >/dev/null 2>&1 && break; sleep 0.1; done
expect() { # status url [curl args...]
  local want=$1 url=$2; shift 2
  local got; got=$(curl -s -o /dev/null -w '%{http_code}' "$@" "$url")
  [ "$got" = "$want" ] || { echo "smoke: $url → $got, want $want"; cat "$log"; exit 1; }
}
expect 200 "$base/healthz"
expect 200 "$base/api/v1/market/natives"
expect 200 "$base/api/v1/smart-money/inflow?window=1h"
expect 403 "$base/api/v1/admin/health"
expect 200 "$base/api/v1/admin/health" -H "Authorization: Bearer smoke-token"
kill -TERM "$pid"
for _ in $(seq 1 50); do kill -0 "$pid" 2>/dev/null || break; sleep 0.1; done
if kill -0 "$pid" 2>/dev/null; then echo "smoke: binary did not stop on SIGTERM"; exit 1; fi
grep -q '"msg":"stopped"' "$log" || { echo "smoke: no clean shutdown log"; cat "$log"; exit 1; }
echo "backend smoke passed on port $port"
