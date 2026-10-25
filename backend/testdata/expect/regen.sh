#!/usr/bin/env bash
# Regenerates every decoder golden file from its real samples with the
# independent Python decoders in this directory and diffs it with the
# committed copy. PUMP_IDL_DIR must point at the official Pump IDLs.
#   backend/testdata/expect/regen.sh          # check only
#   backend/testdata/expect/regen.sh -w       # rewrite goldens
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd); be=$here/../..; tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
z() { zstd -dc "$be/internal/$1" > "$tmp/$(basename "$1" .zst)"; echo "$tmp/$(basename "$1" .zst)"; }
bsc=$be/internal/chaininfo/testdata/bsc_calls.json; base=$be/internal/chaininfo/testdata/base_calls.json
gen() { # golden script args...
  local golden=$be/internal/$1 out=$tmp/out; shift
  python3 -I "$here/$1" "${@:2}" > "$out"
  if [ "${W:-}" = 1 ]; then cp "$out" "$golden"; echo "wrote $golden"
  elif cmp -s "$out" "$golden"; then echo "ok   ${golden#$be/}"
  else echo "DIFF ${golden#$be/}"; diff "$golden" "$out" | head -20; status=1; fi
}
[ "${1:-}" = -w ] && W=1; status=0
gen parse/pump/testdata/expect.txt expect_pump.py "$(z solana/testdata/pump_txs.json.zst)"
gen parse/fourmeme/testdata/expect.txt expect_four.py "$(z evm/testdata/four_meme.json.zst)"
gen parse/pancake/testdata/v2_expect.txt expect_pancake_v2.py "$(z evm/testdata/pancake_v2.json.zst)" "$bsc"
gen parse/pancake/testdata/v3cl_expect.txt expect_pancake_v3cl.py "$(z evm/testdata/pancake_v3_cl.json.zst)" "$bsc"
gen parse/pancake/testdata/liq_expect.txt expect_pancake_liq.py "$(z evm/testdata/pancake_liquidity.json.zst)" "$bsc"
gen parse/clanker/testdata/expect.txt expect_clanker.py "$(z evm/testdata/clanker_v4.json.zst)"
gen parse/doppler/testdata/expect.txt expect_doppler.py "$(z evm/testdata/doppler_hookinit.json.zst)" "$base"
exit $status
