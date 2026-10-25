"""Independent H5 fixture expectations. Run from any directory."""
import json, subprocess
from pathlib import Path
from soltx import token_balances, balance_deltas
root = Path(__file__).resolve().parents[2]
samples = json.loads(subprocess.check_output(['zstd','-dc',str(root/'internal/solana/testdata/pump_txs.json.zst')]))
expected = {}
for s in samples:
    tx = s['tx']
    tracked = sorted({b['mint'] for side in ('pre','post') for b in token_balances(tx,side)} - {'So11111111111111111111111111111111111111112'})
    expected[s['name']] = dict(tracked=tracked, deltas=balance_deltas(tx,tracked))
print(json.dumps(expected,indent=2))
