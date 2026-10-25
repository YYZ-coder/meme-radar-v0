# Independent expectations for the Go Clanker v4 / Uniswap v4 decoder.
import os, sys, json
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from keccak import topic, keccak256
from expect_pancake_v2 import fixed, TR
CLANKER='0xe85a59c628f7d27878aceb4bf3b35733630083a9'; PM='0x498581ff718922c3f8e6a244956af099b2652b2b'
WETH='0x4200000000000000000000000000000000000006'; ZERO='0x'+'0'*40; USDC='0x833589fcd6edb6e08f4c7c32d4f71b54bda02913'
QUOTES={ZERO:('ETH',18),WETH:('ETH',18),USDC:('USDC',6)}
HOOKS=['0x34a45c6b61876d739400bd71228cbcbd4f53e8cc','0xdd5eeaff7bd481ad55db083062b13a3cdf0a68cc','0xd60d6b218116cfd801e28f78d011a203d2b068cc','0xb429d62f8f3bffb98cdb9569533ea23bf0ba28cc']
TC=topic('TokenCreated(address,address,address,string,string,string,string,string,int24,address,bytes32,address,address,address,uint256,address[])')
SW=topic('Swap(bytes32,address,int128,int128,uint160,uint128,int24,uint24)')
def signed(h): x=int(h,16); return x-(1<<256) if x>=1<<255 else x
def pid(c0,c1,h): return '0x'+keccak256(bytes.fromhex(c0[2:].rjust(64,'0'))+bytes.fromhex(c1[2:].rjust(64,'0'))+(0x800000).to_bytes(32,'big')+(200).to_bytes(32,'big')+bytes.fromhex(h[2:].rjust(64,'0'))).hex()
def string(d,off): n=int.from_bytes(d[off:off+32],'big'); return d[off+32:off+32+n].decode('utf-8','replace')
def facts(s):
    tx,r=s['tx'],s['receipt']
    if r['status']=='0x0': return []
    frm=tx['from'].lower(); out=[]; known={}
    for l in r['logs']:
        if l['address'].lower()==CLANKER and l['topics'] and l['topics'][0]==TC:
            d=bytes.fromhex(l['data'][2:]); w=[int.from_bytes(d[i:i+32],'big') for i in range(0,len(d),32)]
            tok='0x'+l['topics'][1][26:]; admin='0x'+l['topics'][2][26:]; poolid='0x%064x'%w[8]; paired='0x%040x'%w[9]
            q=WETH if paired==ZERO else paired; qs=QUOTES.get(paired,('?',18))[0]
            known[poolid]=(tok,paired)
            out.append(f"token {tok} created name={string(d,w[2])} symbol={string(d,w[3])} uri={string(d,w[1])} dec=18 supply= creator={admin} clanker")
            out.append(f"pool {poolid} token={tok} quote={q} {qs} uniswap_v4 created status=active grad=- rt= rq=")
    cands={l['address'].lower() for l in r['logs'] if l['topics'] and l['topics'][0]==TR}|{ZERO}
    for l in r['logs']:
        if not(l['address'].lower()==PM and l['topics'] and l['topics'][0]==SW): continue
        P=l['topics'][1]; li=int(l['logIndex'],16)
        if P in known: tok,quote=known[P]
        else:
            hit=[(a,b) for a in sorted(cands) for b in sorted(cands) for h in HOOKS if a<b and pid(a,b,h)==P]
            if not hit: continue
            a,b=hit[0]
            if (a in QUOTES)==(b in QUOTES): continue
            tok,quote=(b,a) if a in QUOTES else (a,b)
        c0,c1=sorted([tok,quote]); d=l['data'][2:]; a0,a1=signed(d[:64]),signed(d[64:128])
        at,aq=(a0,a1) if tok==c0 else (a1,a0)
        buy=at>0; tokamt,qamt=abs(at),abs(aq)
        delta=0
        for x in r['logs']:
            if x['address'].lower()==tok and x['topics'] and x['topics'][0]==TR and len(x['topics'])==3:
                v=int(x['data'],16)
                if '0x'+x['topics'][2][26:]==frm: delta+=v
                if '0x'+x['topics'][1][26:]==frm: delta-=v
        ok=(buy and delta>0) or (not buy and delta<0)
        qs,qd=QUOTES[quote]; qa=WETH if quote==ZERO else quote
        price=fixed(qamt*10**18,tokamt*10**qd,18) if tokamt else ''
        value=fixed(qamt,10**18,18) if quote in (ZERO,WETH) else ''
        out.append(f"token {tok} seen dec=18 clanker")
        out.append(f"pool {P} token={tok} quote={qa} {qs} uniswap_v4 seen status= grad=- rt= rq=")
        out.append(f"trade {li} pool={P} token={tok} wallet={frm if ok else '-'} {'confirmed' if ok else 'ambiguous'} {'buy' if buy else 'sell'} tok={tokamt} quote={qamt} {qs} price={price} value={value} clanker_v4")
    return out
if __name__=='__main__':
    for s in json.load(open(sys.argv[1]))['samples']:
        print('##',s['name'])
        for l in facts(s): print(l)
