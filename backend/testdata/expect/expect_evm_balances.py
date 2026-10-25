"""Independent ERC-20 Transfer decoding of the recorded Four.meme samples."""
import json, subprocess
from pathlib import Path
from keccak import topic
root=Path(__file__).resolve().parents[2]
rec=json.loads(subprocess.check_output(['zstd','-dc',str(root/'internal/evm/testdata/four_meme.json.zst')]))
transfer=topic('Transfer(address,address,uint256)')
expected={}
for sample in rec['samples']:
    logs=[l for l in sample['receipt']['logs'] if len(l['topics'])==3 and l['topics'][0]==transfer and len(l['data'])==66]
    # WBNB is deliberately outside the tracked set, so routed swaps test filtering.
    tracked=sorted({l['address'].lower() for l in logs}-{'0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c'})
    rows=[]
    for l in logs:
        token=l['address'].lower()
        if token not in tracked: continue
        owners={}
        for tp,sign in [(l['topics'][1],-1),(l['topics'][2],1)]:
            owner='0x'+tp[-40:].lower()
            if sign==-1 and int(owner,16)==0:continue
            owners[owner]=owners.get(owner,0)+sign*int(l['data'],16)
        rows += [f"{int(l['logIndex'],16)}|{token}|{owner}|{n}" for owner,n in owners.items() if n]
    expected[sample['name']]=dict(tracked=tracked,deltas=sorted(rows))
print(json.dumps(expected,indent=2))
