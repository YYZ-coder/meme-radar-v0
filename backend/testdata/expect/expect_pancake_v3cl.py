# Independent expectations for the Go PancakeSwap V3 and Infinity CL decoders.
import os, sys, json
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from keccak import topic, keccak256
from expect_pancake_v2 import fixed, four, erc20, call, WBNB, SYMS, TR
D='41ff9aa7e16b8b1a8a8dc4f0efacd93d02d071c9'; H3='6ce8eb472fa82df5469c6ab6d485f17c3ad13c8cd7af59b3d4a8026c5ce0f7e2'
CLPM='0xa0ffb9c1ce1fe56963b0321b32e7a0302114058b'; ZERO='0x'+'0'*40
SW3=topic('Swap(address,address,int256,int256,uint160,uint128,int24,uint128,uint128)'); CLS=topic('Swap(bytes32,address,int128,int128,uint160,uint128,int24,uint24,uint16)')
def signed(h): x=int(h,16); return x-(1<<256) if x>=1<<255 else x
def v3pool(a,b,fee):
    t0,t1=sorted([a,b]); salt=keccak256(bytes.fromhex(t0[2:].rjust(64,'0'))+bytes.fromhex(t1[2:].rjust(64,'0'))+fee.to_bytes(32,'big'))
    return '0x'+keccak256(b'\xff'+bytes.fromhex(D)+salt+bytes.fromhex(H3))[12:].hex()
def pick(rec,a,b):
    qa,qb=four(rec,a),four(rec,b)
    if qa is None and qb is None: return None
    if qa is not None and qb is not None: return (b,a) if qb==a else (a,b)
    return (a,b) if qa is not None else (b,a)
def emit(out,rec,frm,r,li,P,proto,tok,quote,buy,tokamt,qamt):
    delta=0
    for x in r['logs']:
        if x['address'].lower()==tok and x['topics'] and x['topics'][0]==TR and len(x['topics'])==3:
            v=int(x['data'],16)
            if '0x'+x['topics'][2][26:]==frm: delta+=v
            if '0x'+x['topics'][1][26:]==frm: delta-=v
    ok=(buy and delta>0) or (not buy and delta<0)
    if quote==WBNB: qd,qs=18,'BNB'
    else: qd,qs=erc20(rec,quote); qs=SYMS.get(quote,qs)
    price=fixed(qamt*10**18,tokamt*10**qd,18) if tokamt else ''
    value=fixed(qamt,10**18,18) if quote==WBNB else ''
    out.append(f"token {tok} seen dec=18 four_meme")
    out.append(f"pool {P} token={tok} quote={quote} {qs} {proto} seen status= grad=- rt= rq=")
    out.append(f"trade {li} pool={P} token={tok} wallet={frm if ok else '-'} {'confirmed' if ok else 'ambiguous'} {'buy' if buy else 'sell'} tok={tokamt} quote={qamt} {qs} price={price} value={value} {proto}")
def facts(s,rec):
    tx,r=s['tx'],s['receipt']
    if r['status']=='0x0': return []
    frm=tx['from'].lower(); out=[]
    for l in r['logs']:
        if not l['topics']: continue
        li=int(l['logIndex'],16); d=l['data'][2:]
        if l['topics'][0]==SW3:
            P=l['address'].lower()
            toks={x['address'].lower() for x in r['logs'] if x['topics'] and x['topics'][0]==TR and len(x['topics'])==3 and P in ('0x'+x['topics'][1][26:],'0x'+x['topics'][2][26:])}
            hit=[(a,b) for a in toks for b in toks for fe in (100,500,2500,10000) if a<b and v3pool(a,b,fe)==P]
            if not hit: continue
            t0,t1=hit[0]; pk=pick(rec,t0,t1)
            if not pk: continue
            tok,quote=pk; a0,a1=signed(d[:64]),signed(d[64:128]); at,aq=(a0,a1) if tok==t0 else (a1,a0)
            emit(out,rec,frm,r,li,P,'pancake_v3',tok,quote,at<0,abs(at),abs(aq))
        elif l['address'].lower()==CLPM and l['topics'][0]==CLS:
            k=call(rec,CLPM,'0x0e2d484a'+l['topics'][1][2:])
            c0,c1='0x'+k[24:64],'0x'+k[64+24:128]
            c0=WBNB if c0==ZERO else c0; c1=WBNB if c1==ZERO else c1
            pk=pick(rec,c0,c1)
            if not pk: continue
            tok,quote=pk; a0,a1=signed(d[:64]),signed(d[64:128]); at,aq=(a0,a1) if tok==c0 else (a1,a0)
            emit(out,rec,frm,r,li,l['topics'][1],'pancake_infinity_cl',tok,quote,at>0,abs(at),abs(aq))
    return out
if __name__=='__main__':
    d=json.load(open(sys.argv[1])); rec=json.load(open(sys.argv[2]))
    for s in d['samples']:
        print('##',s['name'])
        for l in facts(s,rec): print(l)
