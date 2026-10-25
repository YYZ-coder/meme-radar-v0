# Independent expectations for PancakeSwap V2/V3 liquidity events of tracked tokens.
# TRACKED replaces the Four.meme check so real non-Four.meme Mint/Burn samples can
# verify the decoding (the tracked side is the pair's non-quote token).
import os, sys, json
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from keccak import topic
from expect_pancake_v2 import fixed, erc20, pair, TR, WBNB, SYMS
from expect_pancake_v3cl import v3pool
M2=topic('Mint(address,uint256,uint256)'); B2=topic('Burn(address,uint256,uint256,address)')
M3=topic('Mint(address,address,int24,int24,uint128,uint256,uint256)'); B3=topic('Burn(address,int24,int24,uint128,uint256,uint256)')
def tracked_of(a,b):
    if a in SYMS and b not in SYMS: return b
    if b in SYMS and a not in SYMS: return a
    return a if a<b else b
def facts(s,rec):
    tx,r=s['tx'],s['receipt']
    if r['status']=='0x0': return []
    frm=tx['from'].lower(); out=[]
    for l in r['logs']:
        if not l['topics'] or l['topics'][0] not in (M2,B2,M3,B3): continue
        P=l['address'].lower(); li=int(l['logIndex'],16); t=l['topics'][0]
        toks={x['address'].lower() for x in r['logs'] if x['topics'] and x['topics'][0]==TR and len(x['topics'])==3 and P in ('0x'+x['topics'][1][26:],'0x'+x['topics'][2][26:])}
        v2=t in (M2,B2)
        hit=[(a,b) for a in toks for b in toks if a<b and (pair(a,b)==P if v2 else any(v3pool(a,b,fe)==P for fe in (100,500,2500,10000)))]
        if not hit: continue
        t0,t1=hit[0]; tok=tracked_of(t0,t1); quote=t1 if tok==t0 else t0
        d=l['data'][2:]; w=[int(d[k:k+64],16) for k in range(0,len(d),64)]
        a0,a1=(w[0],w[1]) if v2 else ((w[2],w[3]) if t==M3 else (w[1],w[2]))
        if a0==0 and a1==0: continue
        at,aq=(a0,a1) if tok==t0 else (a1,a0)
        if quote==WBNB: qs='BNB'
        else: qd,qs=erc20(rec,quote); qs=SYMS.get(quote,qs)
        value=fixed(aq,10**18,18) if quote==WBNB else ''
        out.append(f"liq {li} pool={P} token={tok} wallet={frm} {'add' if t in (M2,M3) else 'remove'} tok={at} quote={aq} {qs} value={value} {'pancake_v2' if v2 else 'pancake_v3'}")
    return out
if __name__=='__main__':
    rec=json.load(open(sys.argv[2]))
    for s in json.load(open(sys.argv[1]))['samples']:
        print('##',s['name'])
        for l in facts(s,rec): print(l)
