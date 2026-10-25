#!/usr/bin/env python3
"""Record real Pump JSON through Pinata; verify CIDv1 raw SHA-256 content.

Candidates were sampled with a read-only cloud SQL query on 2026-10-24:
chain=solana, launch_platform=pump_fun, metadata_uri LIKE '%/ipfs/bafkrei%'.
No Go implementation is used to derive integrity expectations.
"""
import base64
import datetime
import hashlib
import json
from pathlib import Path
import subprocess

GATEWAY = 'https://gateway.pinata.cloud/ipfs/'
CANDIDATES = [
 ('EaQmGzp3DcVhzApmxEx4YXn1wziGBzVCPGjTBVCapump','bafkreih4eo3gyvds4fzuwi62bx6vx7gjke6ve443uqmtn47vt4xbfsfrcy'),
 ('DnNnnVinn4QfiGgMoHE1riKFuBWdT4zY5nRwztxTpump','bafkreifl6cglg6sk4a7gmchpyakkk6vw7bgtzvggihemj662ucwxlylpkm'),
 ('TbU8rWELwCNuFu5wuW2uxgRfzYNfhe1CwgUBGA1tcUh','bafkreiepwba35vkrvm3oih5chdlub4od3tukh3xti2bjs5jlttylgn245y'),
]

def verify(cid, body):
    if cid.startswith('bafkrei'):
        unprefixed = cid[1:].upper()
        wire = base64.b32decode(unprefixed + '=' * (-len(unprefixed) % 8))
        assert wire[:4] == bytes.fromhex('01551220'), wire[:4].hex()
        assert len(wire) == 36
        assert hashlib.sha256(body).digest() == wire[4:], 'CID content mismatch'
        return hashlib.sha256(body).hexdigest()
    assert cid.startswith('Qm'), 'unsupported content-integrity check'
    return None

if __name__ == '__main__':
    root = Path(__file__).resolve().parent
    sources = []
    for n, (mint, cid) in enumerate(CANDIDATES, 1):
        body = subprocess.check_output(['curl','-fsS','--max-time','30','-A','Mozilla/5.0',GATEWAY+cid])
        digest = verify(cid, body)
        assert len(body) <= 512*1024
        obj = json.loads(body)
        assert isinstance(obj,dict)
        filename = f'pump_{n}.json'
        (root/filename).write_bytes(body)
        sources.append(dict(file=filename,mint=mint,cid=cid,original_uri='https://ipfs.io/ipfs/'+cid,gateway=GATEWAY,download_date=datetime.date.today().isoformat(),sha256=digest))
        print(filename, digest, json.dumps(obj,ensure_ascii=False),flush=True)
    (root/'pump_sources.json').write_text(json.dumps(sources,indent=2)+'\n')
