"""Compare stored cloud samples independently from Go; stdin contains public blocks
and tracked address bytes. No database access, credentials, or Go dependencies.
"""
import json, sys, collections
from soltx import b58e, b58d, balance_deltas
from keccak import topic
request=json.load(sys.stdin)
tracked=set(request['tracked'])
transfer=topic('Transfer(address,address,uint256)')
result={}
for block in request['blocks']:
    payload=block['payload']
    rows=[]
    if request['chain']=='solana':
        mints={b58e(bytes.fromhex(a)) for a in tracked}
        for index,tx in enumerate(payload['transactions']):
            for row in balance_deltas(tx,mints):
                owner,mint,delta=row.split('|')
                rows.append(f"{index}|0|{b58d(mint).hex()}|{b58d(owner).hex()}|{delta}")
    else:
        for index,receipt in enumerate(payload['receipts']):
            if receipt['status']=='0x0':continue
            for log in receipt['logs']:
                token=log['address'][2:].lower()
                if token not in tracked or len(log['topics'])!=3 or log['topics'][0]!=transfer or len(log['data'])!=66:continue
                changes=collections.Counter()
                for tp,sign in [(log['topics'][1],-1),(log['topics'][2],1)]:
                    owner=tp[-40:].lower()
                    if sign==-1 and int(owner,16)==0:continue
                    changes[owner]+=sign*int(log['data'],16)
                rows += [f"{index}|{int(log['logIndex'],16)}|{token}|{owner}|{delta}" for owner,delta in changes.items() if delta]
    result[str(block['num'])]=sorted(rows)
json.dump(result,sys.stdout)
