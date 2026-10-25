# Independent expectations for the Go PancakeSwap V2 decoder (rules in 06-parse §7).
import os, sys, json
from decimal import Decimal, ROUND_HALF_UP, getcontext
getcontext().prec=90
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from keccak import topic, keccak256
F='ca143ce32fe78f1f7019d7d551a6402fc5350c73'; H='00fb7f630766e6a796048ea87d01acd3068e8ff67d078148a3fa3f4a84f69bd5'
WBNB='0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c'; SYMS={WBNB:'BNB','0x55d398326f99059ff775485246999027b3197955':'USDT','0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d':'USDC'}
SW=topic('Swap(address,uint256,uint256,uint256,uint256,address)'); TR=topic('Transfer(address,address,uint256)'); SY=topic('Sync(uint112,uint112)')
HELPER='0xf251f83e40a78868fcfa3fa4599dad6494e46034'
def fixed(n,d,p): return format((Decimal(n)/Decimal(d)).quantize(Decimal(1).scaleb(-p),rounding=ROUND_HALF_UP),'f')
def pair(a,b):
    t0,t1=sorted([a,b]); return '0x'+keccak256(b'\xff'+bytes.fromhex(F)+keccak256(bytes.fromhex(t0[2:])+bytes.fromhex(t1[2:]))+bytes.fromhex(H))[12:].hex()
def call(rec,to,data):
    for c in rec:
        if c['to'].lower()==to and c['data'].lower()==data: return c['response']['result'][2:]
def four(rec,t):
    if not t.endswith(('ffff','4444')): return None
    r=call(rec,HELPER,'0x1f69565f'+t[2:].rjust(64,'0'))
    if r is None or int(r[0:64],16)==0: return None
    return '0x'+r[128+24:192]
def erc20(rec,t):
    d=int(call(rec,t,'0x313ce567')[:64],16); s=bytes.fromhex(call(rec,t,'0x95d89b41')); n=int.from_bytes(s[32:64],'big'); return d,s[64:64+n].decode()
def facts(s,rec):
    tx,r=s['tx'],s['receipt']
    if r['status']=='0x0': return []
    frm=tx['from'].lower(); out=[]
    for l in r['logs']:
        if not(l['topics'] and l['topics'][0]==SW and len(l['topics'])==3): continue
        P=l['address'].lower(); li=int(l['logIndex'],16)
        toks={x['address'].lower() for x in r['logs'] if x['topics'] and x['topics'][0]==TR and len(x['topics'])==3 and P in ('0x'+x['topics'][1][26:],'0x'+x['topics'][2][26:])}
        hit=[(a,b) for a in toks for b in toks if a<b and pair(a,b)==P]
        if not hit: continue
        t0,t1=hit[0]; q0,q1=four(rec,t0),four(rec,t1)
        if q0 is None and q1 is None: continue
        if q0 is not None and q1 is not None: tok=t1 if (q1==t0) else t0
        else: tok=t0 if q0 is not None else t1
        quote=t1 if tok==t0 else t0
        d=l['data'][2:]; a0in,a1in,a0out,a1out=[int(d[k:k+64],16) for k in range(0,256,64)]
        tin,tout,qin,qout=(a0in,a0out,a1in,a1out) if tok==t0 else (a1in,a1out,a0in,a0out)
        buy=tout>tin; tokamt=tout-tin if buy else tin-tout; qamt=abs(qin-qout)
        syncs=[x for x in r['logs'] if x['address'].lower()==P and x['topics'] and x['topics'][0]==SY and int(x['logIndex'],16)<li]
        rt=rq=''
        if syncs:
            sd=syncs[-1]['data'][2:]; r0,r1=int(sd[:64],16),int(sd[64:128],16); rt,rq=(r0,r1) if tok==t0 else (r1,r0)
        delta=0
        for x in r['logs']:
            if x['address'].lower()==tok and x['topics'] and x['topics'][0]==TR and len(x['topics'])==3:
                v=int(x['data'],16)
                if '0x'+x['topics'][2][26:]==frm: delta+=v
                if '0x'+x['topics'][1][26:]==frm: delta-=v
        confirmed=(buy and delta>0) or (not buy and delta<0)
        qd,qs=erc20(rec,quote)
        qs=SYMS.get(quote,qs)
        price=fixed(qamt*10**18,tokamt*10**qd,18) if tokamt else ''
        value=fixed(qamt,10**18,18) if quote==WBNB else ''
        out.append(f"token {tok} seen dec=18 four_meme")
        out.append(f"pool {P} token={tok} quote={quote} {qs} pancake_v2 seen status= grad=- rt={rt} rq={rq}")
        out.append(f"trade {li} pool={P} token={tok} wallet={frm if confirmed else '-'} {'confirmed' if confirmed else 'ambiguous'} {'buy' if buy else 'sell'} tok={tokamt} quote={qamt} {qs} price={price} value={value} pancake_v2")
    return out
if __name__=='__main__':
    d=json.load(open(sys.argv[1])); rec=json.load(open(sys.argv[2]))
    for s in d['samples']:
        print('##',s['name'])
        for l in facts(s,rec): print(l)
