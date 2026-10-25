#!/usr/bin/env bash
# Usage: run.sh <slice> <red|green> <go test -run regex> <test files...>
set -uo pipefail
slice=$1; phase=$2; pattern=$3; shift 3
dir=$(cd "$(dirname "$0")" && pwd)
cd "$dir/../../.."
{ echo "# $slice $phase $(date -u +%FT%TZ) base=$(git rev-parse --short HEAD)"; shasum -a 256 "$@"; echo "\$ go test -count=1 -run '$pattern' ./internal/httpapi/"; go test -count=1 -run "$pattern" ./internal/httpapi/ 2>&1; echo "exit=$?"; } > "$dir/$slice-$phase.log"
grep -E '^(--- FAIL|--- PASS|ok|FAIL|exit=)|want|violates' "$dir/$slice-$phase.log" | head -${LINES_OUT:-14}
