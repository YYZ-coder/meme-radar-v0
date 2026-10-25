#!/usr/bin/env python3
"""Independent ABI-string decoding of the recorded tokenURI() eth_call bytes."""
import json
from pathlib import Path
p=Path(__file__).resolve().parents[2]/'internal/metadata/testdata/doppler_token_uri.json'
for row in json.loads(p.read_text())['samples']:
    data=bytes.fromhex(row['response']['result'][2:])
    offset=int.from_bytes(data[:32],'big')
    length=int.from_bytes(data[offset:offset+32],'big')
    uri=data[offset+32:offset+32+length].decode('utf-8')
    assert uri == row['uri']
    print(row['address'],uri)
