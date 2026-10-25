#!/usr/bin/env bash
# Deletes raw blocks older than a UTC date by hand, ahead of the automatic
# deletion after retention.raw_blocks_days (30, D150), e.g. when the disk
# runs short. Facts (trades, pools, tokens) are kept.
#   scripts/prune-raw.sh 2026-10-01          # whole days of every chain
#   scripts/prune-raw.sh 2026-10-01 bsc      # one chain's rows only
#   scripts/prune-raw.sh 2026-10-01 bsc -y   # without the confirmation prompt
set -euo pipefail
before=${1:?usage: scripts/prune-raw.sh YYYY-MM-DD [chain] [-y]}
chain=${2:-}
[ "$chain" = "-y" ] && { chain=""; yes=1; } || yes=${3:+1}
cd "$(dirname "$0")/.."
test -f .env || { echo "missing .env (RADAR_DATABASE_URL)"; exit 1; }
args=(--before="$before")
[ -n "$chain" ] && args+=(--chain="$chain")
if [ -z "${yes:-}" ]; then
  read -r -p "Delete raw blocks before $before (${chain:-all chains})? [y/N] " ok
  [ "$ok" = y ] || { echo "aborted"; exit 1; }
fi
docker compose --profile ingest run --rm --no-deps --entrypoint radar compute prune-raw "${args[@]}"
