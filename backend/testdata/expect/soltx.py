# Independent prototype (not the Go implementation): wire parse + log/instruction
# matching + Borsh event decode for Pump.fun / PumpSwap, used to derive test expectations.
import json, base64, struct, sys, glob, collections
A='123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz'
def b58e(b):
    n=int.from_bytes(b,'big'); s=''
    while n: n,r=divmod(n,58); s=A[r]+s
    return '1'*(len(b)-len(b.lstrip(b'\0')))+s
def b58d(s):
    n=0
    for c in s: n=n*58+A.index(c)
    body=n.to_bytes((n.bit_length()+7)//8,'big') if n else b''
    return b'\0'*(len(s)-len(s.lstrip('1')))+body
def sv(b,i):
    v=0;s=0
    while True:
        c=b[i];i+=1;v|=(c&0x7f)<<s;s+=7
        if c<0x80: return v,i
def parse_wire(raw):
    if raw[0]==0x81:
        nreq=raw[1]; i=4; mask=struct.unpack_from('<I',raw,i)[0]; i+=4; i+=32
        nix=raw[i]; nadd=raw[i+1]; i+=2
        keys=[raw[i+32*k:i+32*k+32] for k in range(nadd)]; i+=32*nadd
        i+=4*bin(mask).count('1')
        hdr=[]
        for _ in range(nix): hdr.append(struct.unpack_from('<BBH',raw,i)); i+=4
        ixs=[]
        for p,na,nd in hdr:
            acc=list(raw[i:i+na]); i+=na; data=raw[i:i+nd]; i+=nd; ixs.append((p,acc,data))
        sig=raw[len(raw)-64*nreq:len(raw)-64*(nreq-1)]
        return sig,nreq,keys,ixs
    nsig,i=sv(raw,0); sig=raw[i:i+64]; i+=64*nsig
    if raw[i]&0x80: i+=1
    nreq=raw[i]; i+=3
    nk,i=sv(raw,i); keys=[raw[i+32*k:i+32*k+32] for k in range(nk)]; i+=32*nk; i+=32
    nix,i=sv(raw,i); ixs=[]
    for _ in range(nix):
        p=raw[i]; i+=1; na,i=sv(raw,i); acc=list(raw[i:i+na]); i+=na; nd,i=sv(raw,i); data=raw[i:i+nd]; i+=nd; ixs.append((p,acc,data))
    return sig,nreq,keys,ixs
def tx_model(tx):
    raw=base64.b64decode(tx['transaction'][0]); sig,nreq,keys,ixs=parse_wire(raw)
    la=tx['meta'].get('loadedAddresses') or {}
    keys=keys+[b58d(x) for x in la.get('writable',[])]+[b58d(x) for x in la.get('readonly',[])]
    inner={g['index']:g['instructions'] for g in tx['meta'].get('innerInstructions') or []}
    order=[]
    for t,(p,acc,data) in enumerate(ixs):
        order.append(dict(top=t,depth=1,prog=b58e(keys[p]),acc=[b58e(keys[a]) for a in acc],data=data))
        for ii in inner.get(t,[]):
            order.append(dict(top=t,depth=ii.get('stackHeight') or 2,prog=b58e(keys[ii['programIdIndex']]),acc=[b58e(keys[a]) for a in ii['accounts']],data=b58d(ii['data'])))
    return dict(sig=b58e(sig),signers=[b58e(k) for k in keys[:nreq]],ixs=order,logs=tx['meta'].get('logMessages') or [],err=tx['meta']['err'],meta=tx['meta'],keys=[b58e(k) for k in keys])
def attach(m):
    """Map 'Program data:' logs to instructions in execution order; None if logs disagree."""
    stack=[]; k=0; ev=collections.defaultdict(list)
    for l in m['logs']:
        if l.startswith('Program ') and ' invoke [' in l:
            prog=l.split()[1]
            if k>=len(m['ixs']) or m['ixs'][k]['prog']!=prog: return None
            stack.append(k); k+=1
        elif l.startswith('Program data: ') and stack: ev[stack[-1]].append(base64.b64decode(l[14:]))
        elif l.startswith('Program ') and (l.endswith(' success') or ' failed' in l) and stack:
            stack.pop()
        elif l=='Log truncated': return None
    return ev if k==len(m['ixs']) else None
class R:
    def __init__(s,b): s.b=b; s.i=0
    def left(s): return len(s.b)-s.i
    def u8(s): v=s.b[s.i]; s.i+=1; return v
    def u16(s): v=struct.unpack_from('<H',s.b,s.i)[0]; s.i+=2; return v
    def u64(s): v=struct.unpack_from('<Q',s.b,s.i)[0]; s.i+=8; return v
    def i64(s): v=struct.unpack_from('<q',s.b,s.i)[0]; s.i+=8; return v
    def pk(s): v=b58e(s.b[s.i:s.i+32]); s.i+=32; return v
    def str(s): n=struct.unpack_from('<I',s.b,s.i)[0]; s.i+=4; v=s.b[s.i:s.i+n].decode('utf-8','replace'); s.i+=n; return v
    def boolean(s): return s.u8()!=0
# --- program derived addresses (independent of the Go implementation) ---
import hashlib
_P=2**255-19; _D=(-121665*pow(121666,_P-2,_P))%_P
def on_curve(b):
    y=int.from_bytes(b,'little')&((1<<255)-1)
    if y>=_P: return False
    u=(y*y-1)%_P; v=(_D*y*y+1)%_P
    x2=u*pow(v,_P-2,_P)%_P
    if x2==0: return True
    return pow(x2,(_P-1)//2,_P)==1
def find_pda(seeds,program):
    for bump in range(255,-1,-1):
        h=hashlib.sha256(b''.join(seeds)+bytes([bump])+b58d(program)+b'ProgramDerivedAddress').digest()
        if not on_curve(h): return b58e(h),bump


def token_balances(tx, side):
    """Recorded RPC meta, without deriving expectations through Go."""
    return [dict(mint=b['mint'], owner=b['owner'], amount=b['uiTokenAmount']['amount'],
                 account_index=b['accountIndex'])
            for b in tx['meta'].get(side+'TokenBalances') or [] if b.get('owner')]

def balance_deltas(tx, tracked):
    """Integer sum of RPC meta per owner/mint, independent of protocol events."""
    amounts = collections.Counter()
    if tx['meta']['err'] is not None:
        return []
    for side, sign in [('pre', -1), ('post', 1)]:
        for b in token_balances(tx, side):
            if b['mint'] in tracked:
                amounts[(b['owner'], b['mint'])] += sign * int(b['amount'])
    return sorted(f"{owner}|{mint}|{delta}" for (owner,mint),delta in amounts.items() if delta)

if __name__ == '__main__':
    import subprocess
    from pathlib import Path
    root = Path(__file__).resolve().parents[2]
    block = json.loads(subprocess.check_output(['zstd', '-dc', str(root/'internal/ingest/testdata/solana_getblock_460261066.json.zst')]))['result']
    expected = {str(i): token_balances(block['transactions'][i], 'pre') for i in (2,7,9)}
    print(json.dumps(expected, indent=2))
