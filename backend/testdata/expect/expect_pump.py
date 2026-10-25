# Independent expectations for the Go pump decoder: generic IDL-driven Borsh
# decoding + the fact rules stated in docs (06-parse), printed as canonical lines.
import os, sys, json, struct
from decimal import Decimal, ROUND_HALF_UP, getcontext
getcontext().prec=80
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from soltx import tx_model, b58e, b58d, find_pda
PUMP='6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P'; AMM='pAMMBay6oceH9fJKBRHGP5D4bD4sWpmSwMn52FMfXEA'; TAG='e445a52e51cb9a1d'
WSOL='So11111111111111111111111111111111111111112'; ZERO='11111111111111111111111111111111'
SYM={WSOL:'SOL','EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v':'USDC','Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB':'USDT'}
# Official IDLs (github.com/pump-fun/pump-public-docs idl/pump.json, idl/pump_amm.json) are
# third-party and not redistributed: pass their directory in PUMP_IDL_DIR.
idl={f:json.load(open(os.path.join(os.environ['PUMP_IDL_DIR'],f'{f}.json'))) for f in ('pump','pump_amm')}
class EOF_(Exception): pass
def dec(t,b,i,types):
    def need(n):
        if i+n>len(b): raise EOF_()
    if isinstance(t,str):
        sz={'u8':1,'bool':1,'u16':2,'u32':4,'u64':8,'i64':8,'i128':16,'pubkey':32}
        if t=='string':
            need(4); n=struct.unpack_from('<I',b,i)[0]; need(4+n); return b[i+4:i+4+n].decode(), i+4+n
        need(sz[t]); raw=b[i:i+sz[t]]
        if t=='pubkey': return b58e(raw), i+32
        if t=='bool': return raw[0]!=0, i+1
        return int.from_bytes(raw,'little',signed=t.startswith('i')), i+sz[t]
    if 'vec' in t:
        need(4); n=struct.unpack_from('<I',b,i)[0]; i+=4; out=[]
        for _ in range(n): v,i=dec(t['vec'],b,i,types); out.append(v)
        return out,i
    if 'defined' in t:
        d={}
        for f in types[t['defined']['name']]['type']['fields']:
            d[f['name']],i=dec(f['type'],b,i,types)
        return d,i
    raise ValueError(t)
def event(f,data):
    for e in idl[f]['events']:
        if bytes(e['discriminator'])==data[:8]:
            types={t['name']:t for t in idl[f]['types']}; d={}; i=8
            for fld in types[e['name']]['type']['fields']:
                try: d[fld['name']],i=dec(fld['type'],data,i,types)
                except EOF_: break
            return e['name'],d
    return None,None
def ixname(f,data):
    for ix in idl[f]['instructions']:
        if bytes(ix['discriminator'])==data[:8]: return ix['name'],[a['name'] for a in ix['accounts']]
    return None,None
def ratio(n,d,places):
    if d==0: return ''
    return format((Decimal(n)/Decimal(d)).quantize(Decimal(1).scaleb(-places),rounding=ROUND_HALF_UP),'f')
def facts(tx):
    m=tx_model(tx)
    if tx['meta']['err'] is not None: return []
    decs={}
    for b in (tx['meta'].get('preTokenBalances') or [])+(tx['meta'].get('postTokenBalances') or []): decs[b['mint']]=b['uiTokenAmount']['decimals']
    post={(b['owner'],b['mint']):b['uiTokenAmount']['amount'] for b in tx['meta'].get('postTokenBalances') or [] if b.get('owner')}
    out=[]
    def price(tok,q,token,quote,tdec):
        qdec=9 if quote==WSOL else decs.get(quote)
        if tdec is None or qdec is None or tok==0: return ''
        return ratio(q*10**tdec, tok*10**qdec, 18)
    pools={}; lastev={}
    for k,ix in enumerate(m['ixs']):
        if ix['prog']!=AMM or ix['data'][:8].hex()!=TAG or ix['depth']<2: continue
        j=k-1
        while j>=0 and not (m['ixs'][j]['top']==ix['top'] and m['ixs'][j]['depth']==ix['depth']-1 and m['ixs'][j]['prog']==ix['prog']): j-=1
        name,e=event('pump_amm',ix['data'][8:])
        if name not in ('BuyEvent','SellEvent','DepositEvent','WithdrawEvent','CreatePoolEvent'): continue
        iname,accs=ixname('pump_amm',m['ixs'][j]['data']); acc=dict(zip(accs or [],m['ixs'][j]['acc']))
        if name=='CreatePoolEvent': pools[e['pool']]=(e['base_mint'],e['quote_mint'])
        elif m['ixs'][j]['acc'][0]==e['pool'] and 'base_mint' in acc: pools.setdefault(e['pool'],(acc['base_mint'],acc['quote_mint']))
        lastev[e['pool']]=(name,e)
    for pool,(name,e) in lastev.items():
        if pool in pools: continue
        # Layout-independent fallback (e.g. multi_hop_swap): the pool's two vaults
        # after the transaction; base is the one at the last event's reserve -/+ amount.
        vaults={mint:int(a) for (owner,mint),a in post.items() if owner==pool}
        delta=-e['base_amount_out'] if name in ('BuyEvent','WithdrawEvent') else e['base_amount_in']
        bases=[mi for mi,a in vaults.items() if a==e['pool_base_token_reserves']+delta]
        if len(vaults)==2 and len(bases)==1: pools[pool]=(bases[0],[mi for mi in vaults if mi!=bases[0]][0])
    for k,ix in enumerate(m['ixs']):
        if ix['prog'] not in (PUMP,AMM) or ix['data'][:8].hex()!=TAG or ix['depth']<2: continue
        j=k-1
        while j>=0 and not (m['ixs'][j]['top']==ix['top'] and m['ixs'][j]['depth']==ix['depth']-1 and m['ixs'][j]['prog']==ix['prog']): j-=1
        em=m['ixs'][j]; f='pump' if ix['prog']==PUMP else 'pump_amm'
        name,e=event(f,ix['data'][8:]); iname,accs=ixname(f,em['data'])
        acc=dict(zip(accs or [],em['acc']))
        signed=lambda u: 'confirmed' if u in m['signers'] else 'inferred'
        if f=='pump' and name=='CreateEvent':
            q=e.get('quote_mint',ZERO); q=WSOL if q==ZERO else q
            tdec=decs.get(e['mint'],6)
            out.append(f"token {e['mint']} created name={e['name']} symbol={e['symbol']} uri={e['uri']} dec={tdec} supply={e['token_total_supply']} creator={e['creator']} pump_fun")
            out.append(f"pool {e['bonding_curve']} token={e['mint']} quote={q} {SYM.get(q,'')} pump_curve created status=active grad=- rt={e['real_token_reserves']} rq=0")
        elif f=='pump' and name=='TradeEvent':
            q=e.get('quote_mint',ZERO); native = q==ZERO
            quote=WSOL if native else q; qa=e['sol_amount'] if native else e['quote_amount']; rq=e['real_sol_reserves'] if native else e['real_quote_reserves']
            # Instructions without a bonding_curve account (multi_hop_curve_swap): derive the PDA.
            curve=acc['bonding_curve'] if 'bonding_curve' in acc else find_pda([b'bonding-curve',b58d(e['mint'])],PUMP)[0]
            tdec=decs.get(e['mint'],6)
            out.append(f"token {e['mint']} seen dec={tdec} pump_fun")
            out.append(f"pool {curve} token={e['mint']} quote={quote} {SYM.get(quote,'')} pump_curve seen status= grad=- rt={e['real_token_reserves']} rq={rq}")
            out.append(f"trade {k} pool={curve} token={e['mint']} wallet={e['user']} {signed(e['user'])} {'buy' if e['is_buy'] else 'sell'} tok={e['token_amount']} quote={qa} {SYM.get(quote,'')} price={price(e['token_amount'],qa,e['mint'],quote,tdec)} value={ratio(qa,10**9,9) if quote==WSOL else ''} pump_fun")
        elif f=='pump' and name=='CompleteEvent':
            q=e.get('quote_mint',ZERO); q=WSOL if q==ZERO else q
            out.append(f"pool {e['bonding_curve']} token={e['mint']} quote={q} {SYM.get(q,'')} pump_curve seen status=graduated grad=- rt= rq=")
        elif f=='pump' and name=='CompletePumpAmmMigrationEvent':
            q=e.get('quote_mint',ZERO); q=WSOL if q==ZERO else q
            out.append(f"pool {e['bonding_curve']} token={e['mint']} quote={q} {SYM.get(q,'')} pump_curve seen status=graduated grad={e['pool']} rt= rq=")
        elif f=='pump_amm' and name in ('BuyEvent','SellEvent','DepositEvent','WithdrawEvent','CreatePoolEvent'):
            pool=e['pool']
            if pool not in pools: out.append(f"failure {k}"); continue
            base,quote=pools[pool]
            # Token perspective: a pool whose base is a quote asset tracks its quote mint.
            flip = base in SYM and quote not in SYM
            token,q = (quote,base) if flip else (base,quote)
            if name=='CreatePoolEvent': tdec=e['quote_mint_decimals'] if flip else e['base_mint_decimals']
            else: tdec=decs.get(token)
            rt=post.get((pool,token),''); rq=post.get((pool,q),'')
            base_amt={'BuyEvent':'base_amount_out','SellEvent':'base_amount_in','DepositEvent':'base_amount_in','WithdrawEvent':'base_amount_out','CreatePoolEvent':'base_amount_in'}[name]
            quote_amt={'BuyEvent':'quote_amount_in','SellEvent':'quote_amount_out','DepositEvent':'quote_amount_in','WithdrawEvent':'quote_amount_out','CreatePoolEvent':'quote_amount_in'}[name]
            tok,qa=(e[quote_amt],e[base_amt]) if flip else (e[base_amt],e[quote_amt])
            base,quote=token,q
            out.append(f"token {base} seen dec={'' if tdec is None else tdec} -")
            out.append(f"pool {pool} token={base} quote={quote} {SYM.get(quote,'')} pumpswap {'created' if name=='CreatePoolEvent' else 'seen'} status={'active' if name=='CreatePoolEvent' else ''} grad=- rt={rt} rq={rq}")
            if name in ('BuyEvent','SellEvent'):
                buy=(name=='BuyEvent')!=flip
                out.append(f"trade {k} pool={pool} token={base} wallet={e['user']} {signed(e['user'])} {'buy' if buy else 'sell'} tok={tok} quote={qa} {SYM.get(quote,'')} price={price(tok,qa,base,quote,tdec)} value={ratio(qa,10**9,9) if quote==WSOL else ''} pumpswap")
            else:
                add=name in ('DepositEvent','CreatePoolEvent'); user=e['creator'] if name=='CreatePoolEvent' else e['user']
                out.append(f"liq {k} pool={pool} token={base} wallet={user} {'add' if add else 'remove'} tok={tok} quote={qa} {SYM.get(quote,'')} value={ratio(qa,10**9,9) if quote==WSOL else ''} pumpswap")
    return out
if __name__=='__main__':
    for e in json.load(open(sys.argv[1])):
        print('##', e['name'])
        for l in facts(e['tx']): print(l)
