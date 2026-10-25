#!/usr/bin/env python3
"""Independent Borsh/PDA expectations for recorded Metaplex account bytes.

The fixed MetadataV1 prefix is key:u8, authority:pubkey, mint:pubkey,
then name/symbol/uri as little-endian u32 byte-length strings. PDA math is
from the existing independent Python prototype soltx.find_pda, not Go.
"""
import base64
import json
from pathlib import Path
import struct
import unicodedata
from soltx import b58d, b58e, find_pda

PROGRAM = 'metaqbxxUerdq28cj1RbAWkYQm3ybzjb6a8bt518x1s'
ROOT = Path(__file__).resolve().parents[2]
SAMPLES = ROOT / 'internal/metadata/testdata/metaplex_accounts.json'

def decode(account):
    data = base64.b64decode(account['response']['result']['value']['data'][0])
    assert data[0] == 4
    assert b58e(data[33:65]) == account['mint']
    i = 65
    fields = {}
    for field, limit in [('name', 128), ('symbol', 32), ('uri', None)]:
        size, = struct.unpack_from('<I', data, i)
        i += 4
        value = data[i:i+size].decode('utf-8', errors='replace')
        i += size
        value = ''.join(c for c in value if unicodedata.category(c) != 'Cc').strip()
        fields[field] = value if limit is None else value[:limit]
    pda, bump = find_pda([b'metadata', b58d(PROGRAM), b58d(account['mint'])], PROGRAM)
    assert (pda, bump) == (account['pda'], account['bump'])
    return dict(mint=account['mint'], pda=pda, bump=bump, **fields)

if __name__ == '__main__':
    expected = [decode(a) for a in json.loads(SAMPLES.read_text())['samples']]
    assert len(expected) == 2
    print(json.dumps(expected, ensure_ascii=False, indent=2))
