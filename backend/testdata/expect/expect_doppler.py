# Independent expectations for the Go Doppler v4 decoder (06-parse §7).
import os, sys, json, itertools
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from keccak import topic, keccak256
from expect_pancake_v2 import fixed, TR, call
PM='0x498581ff718922c3f8e6a244956af099b2652b2b'; AIR='0x660eaaedebc968f8f3694354fa8ec0b4c5ba8d12'
WETH='0x4200000000000000000000000000000000000006'; ZERO='0x'+'0'*40; USDC='0x833589fcd6edb6e08f4c7c32d4f71b54bda02913'
SYMS={ZERO:'ETH',WETH:'ETH',USDC:'USDC'}
STATIC=['0xbdf938149ac6a781f94faa0ed45e6a0e984c6544','0xbb7784a4d481184283ed89619a3e3ed143e1adc0','0x5f9eb5f6726fe88d5e39867967f5b833d2fa3215','0x1e40b0875dda35f41e15cfb475403859b8c860c4','0x660740d7d6fb2c8998fa3fff459cceb9ac12c84b']
FEES=[0x800000,100,500,3000,10000]; SPACINGS=[1,10,60,100,200]
SW=topic('Swap(bytes32,address,int128,int128,uint160,uint128,int24,uint24)')
def signed(h): x=int(h,16); return x-(1<<256) if x>=1<<255 else x
def pid(c0,c1,fee,ts,h): return '0x'+keccak256(bytes.fromhex(c0[2:].rjust(64,'0'))+bytes.fromhex(c1[2:].rjust(64,'0'))+fee.to_bytes(32,'big')+ts.to_bytes(32,'big')+bytes.fromhex(h[2:].rjust(64,'0'))).hex()
def asset(rec,t):
    if t in SYMS: return None
    r=call(rec,AIR,'0x1652e7b7'+t[2:].rjust(64,'0'))
    if r is None or int(r[256:320],16)==0: return None
    return '0x'+r[24:64]
def erc20(rec,t):
    d=int(call(rec,t,'0x313ce567')[:64],16); s=bytes.fromhex(call(rec,t,'0x95d89b41')); n=int.from_bytes(s[32:64],'big'); return d,s[64:64+n].decode()
KNOWN={}  # PoolId -> (c0, c1), remembered across transactions in order
def numeraire(rec,t):
    r=call(rec,AIR,'0x1652e7b7'+t[2:].rjust(64,'0')) if t not in SYMS else None
    if r is None or int(r[256:320],16)==0: return None
    return '0x'+r[24:64]
def facts(s,rec):
    tx,r=s['tx'],s['receipt']
    if r['status']=='0x0': return []
    frm=tx['from'].lower(); out=[]
    base={l['address'].lower() for l in r['logs'] if l['topics'] and l['topics'][0]==TR}|{ZERO,WETH,USDC}
    cands=sorted(base|{n for c in base for n in [numeraire(rec,c)] if n})
    for l in r['logs']:
        if not(l['address'].lower()==PM and l['topics'] and l['topics'][0]==SW): continue
        P=l['topics'][1]; li=int(l['logIndex'],16); hit=KNOWN.get(P)
        if not hit:
            for a,b in itertools.combinations(cands,2):
                if any(pid(a,b,fee,ts,h)==P for h in STATIC for fee in FEES for ts in SPACINGS): hit=(a,b); break
        if not hit: continue
        KNOWN[P]=hit
        a,b=hit; na,nb=asset(rec,a),asset(rec,b)
        if (na is None)==(nb is None): continue
        tok,quote=(a,b) if na is not None else (b,a)
        d=l['data'][2:]; a0,a1=signed(d[:64]),signed(d[64:128]); at,aq=(a0,a1) if tok==a else (a1,a0)
        buy=at>0; tokamt,qamt=abs(at),abs(aq)
        delta=0
        for x in r['logs']:
            if x['address'].lower()==tok and x['topics'] and x['topics'][0]==TR and len(x['topics'])==3:
                v=int(x['data'],16)
                if '0x'+x['topics'][2][26:]==frm: delta+=v
                if '0x'+x['topics'][1][26:]==frm: delta-=v
        ok=(buy and delta>0) or (not buy and delta<0)
        if quote in (ZERO,WETH): qd,qs,qa=18,'ETH',WETH
        else: qd,qs=erc20(rec,quote); qs=SYMS.get(quote,qs); qa=quote
        price=fixed(qamt*10**18,tokamt*10**qd,18) if tokamt else ''
        value=fixed(qamt,10**18,18) if qa==WETH else ''
        out.append(f"token {tok} seen dec=18 doppler")
        out.append(f"pool {P} token={tok} quote={qa} {qs} uniswap_v4 seen status= grad=- rt= rq=")
        out.append(f"trade {li} pool={P} token={tok} wallet={frm if ok else '-'} {'confirmed' if ok else 'ambiguous'} {'buy' if buy else 'sell'} tok={tokamt} quote={qamt} {qs} price={price} value={value} doppler")
    return out
if __name__=='__main__':
    rec=json.load(open(sys.argv[2]))
    for s in json.load(open(sys.argv[1]))['samples']:
        print('##',s['name'])
        for l in facts(s,rec): print(l)
