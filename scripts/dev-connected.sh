#!/usr/bin/env bash
# Run the Go mock API and the Next.js dev server proxying /api/v1 to it.
# Ports: RADAR_API_PORT (default 8080), RADAR_WEB_PORT (default 3000).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
api_port=${RADAR_API_PORT:-8080}
web_port=${RADAR_WEB_PORT:-3000}
make -s -C "$ROOT/backend" build
"$ROOT/backend/bin/radar" api --store=mock --addr="127.0.0.1:$api_port" &
api=$!
trap 'kill "$api" 2>/dev/null || true; wait "$api" 2>/dev/null || true' EXIT INT TERM
for _ in $(seq 1 50); do curl -fsS "http://127.0.0.1:$api_port/healthz" >/dev/null 2>&1 && break; sleep 0.1; done
echo "Go mock API:  http://127.0.0.1:$api_port"
echo "Frontend:     http://127.0.0.1:$web_port/smart-money/inflow  (admin token: mock-admin)"
cd "$ROOT/frontend"
RADAR_API_ORIGIN="http://127.0.0.1:$api_port" npx next dev --hostname 127.0.0.1 --port "$web_port"
