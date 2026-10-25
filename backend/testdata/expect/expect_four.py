# Independent expectations for the Go Four.meme decoder, from the official ABIs
# (github.com/four-meme-community/fourmeme-docs, MIT) and docs/engineering/06-parse.md.
import os, sys, json
from decimal import Decimal, ROUND_HALF_UP, getcontext
getcontext().prec=90
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from keccak import topic
TM1='0xec4549cadce5da21df6e6422d448034b5233bfbc'; TM2='0x5c952063c7fc8610ffdb798152d69f0b9550762b'
WBNB='0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c'; ZERO='0x'+'0'*40
QUOTES={WBNB:'BNB',ZERO:'BNB','0x55d398326f99059ff775485246999027b3197955':'USDT','0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d':'USDC'}
SIG={'create':'TokenCreate(address,address,uint256,string,string,uint256,uint256,uint256)',
     'buy2':'TokenPurchase(address,address,uint256,uint256,uint256,uint256,uint256,uint256)','sell2':'TokenSale(address,address,uint256,uint256,uint256,uint256,uint256,uint256)',
     'buy1':'TokenPurchase(address,address,uint256,uint256,uint256)','sell1':'TokenSale(address,address,uint256,uint256,uint256)',
     'added':'LiquidityAdded(address,uint256,address,uint256)','stop':'TradeStop(address)'}
T={topic(v):k for k,v in SIG.items()}
def fixed(n,d,places):
    return format((Decimal(n)/Decimal(d)).quantize(Decimal(1).scaleb(-places),rounding=ROUND_HALF_UP),'f')
def words(data): d=bytes.fromhex(data[2:]); return d,[int.from_bytes(d[i:i+32],'big') for i in range(0,len(d),32)]
def addr(w): return '0x%040x'%w
def string(d,off):
    n=int.from_bytes(d[off:off+32],'big'); return d[off+32:off+32+n].decode('utf-8','replace')
def facts(sample, info, erc20):
    tx,r=sample['tx'],sample['receipt']
    if int(r['status'],16)!=1: return []
    frm=tx['from'].lower(); out=[]
    def quote_of(token):
        res=info[token]['result'][2:]; q=addr(int(res[128:192],16))
        q=WBNB if q==ZERO else q
        if q in QUOTES: return q,QUOTES[q],18
        e=erc20[q]; s=bytes.fromhex(e['95d89b41']['result'][2:]); n=int.from_bytes(s[32:64],'big')
        return q,s[64:64+n].decode(),int(e['313ce567']['result'],16)
    for l in r['logs']:
        a=l['address'].lower(); k=T.get(l['topics'][0]) if l['topics'] else None
        if a not in (TM1,TM2) or not k: continue
        # V1 graduates with TradeStop (no LiquidityAdded in 46,689 V1 logs); V2 with LiquidityAdded.
        if (a==TM1)!=(k in ('buy1','sell1','stop')) and k not in ('create',): continue
        li=int(l['logIndex'],16); d,w=words(l['data'])
        if k=='create':
            tok=addr(w[1]); q,qs,qd=quote_of(tok)
            out.append(f"token {tok} created name={string(d,w[3])} symbol={string(d,w[4])} uri= dec=18 supply={w[5]} creator={addr(w[0])} four_meme")
            out.append(f"pool {tok} token={tok} quote={q} {qs} four_meme_curve created status=active grad=- rt= rq=")
        elif k in ('buy2','sell2','buy1','sell1'):
            tok=addr(w[0]); acct=addr(w[1])
            if k in ('buy2','sell2'): amount,cost,offers,funds=w[3],w[4],w[6],w[7]; rt,rq=str(offers),str(funds)
            else: amount,cost=w[2],w[3]; rt=rq=''
            q,qs,qd=quote_of(tok)
            out.append(f"token {tok} seen dec=18 four_meme")
            out.append(f"pool {tok} token={tok} quote={q} {qs} four_meme_curve seen status= grad=- rt={rt} rq={rq}")
            price=fixed(cost*10**18, amount*10**qd, 18) if amount else ''
            value=fixed(cost,10**18,18) if q==WBNB else ''
            out.append(f"trade {li} pool={tok} token={tok} wallet={acct} {'confirmed' if acct==frm else 'inferred'} {'buy' if k.startswith('buy') else 'sell'} tok={amount} quote={cost} {qs} price={price} value={value} four_meme")
        elif k in ('added','stop'):
            tok=addr(w[0]); q,qs,qd=quote_of(tok)
            out.append(f"pool {tok} token={tok} quote={q} {qs} four_meme_curve seen status=graduated grad=- rt= rq=")
    return out
if __name__=='__main__':
    d=json.load(open(sys.argv[1]))
    for s in d['samples']:
        print('##',s['name'])
        for l in facts(s,d['getTokenInfo'],d['erc20']): print(l)
