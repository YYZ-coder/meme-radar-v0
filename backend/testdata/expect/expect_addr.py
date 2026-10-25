# Independent base58 (Bitcoin alphabet) expectations for the SQL addr()/from_addr() helpers.
A='123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz'
def b58(b):
    n=int.from_bytes(b,'big'); s=''
    while n: n,r=divmod(n,58); s=A[r]+s
    return '1'*(len(b)-len(b.lstrip(b'\0')))+s
def unb58(s):
    n=0
    for c in s: n=n*58+A.index(c)
    body=n.to_bytes((n.bit_length()+7)//8,'big') if n else b''
    return b'\0'*(len(s)-len(s.lstrip('1')))+body
for s in ['11111111111111111111111111111111','6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P','pAMMBay6oceH9fJKBRHGP5D4bD4sWpmSwMn52FMfXEA','So11111111111111111111111111111111111111112']:
    b=unb58(s); assert b58(b)==s and len(b)==32; print(s,b.hex())
sig=bytes(range(1,65)); print('sig',sig.hex(),b58(sig))
print('lead',b58(bytes([0,0,1])))
