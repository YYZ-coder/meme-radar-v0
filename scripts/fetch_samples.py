#!/usr/bin/env python3
"""Fetch real sample blocks from public RPC endpoints for the design demos.

Read-only, no API keys. Full blocks go to .local/samples/ (git-ignored);
trimmed excerpts used by docs/demos/ go to docs/demos/samples/.

Usage: python3 scripts/fetch_samples.py [solana|base|bsc ...]
"""

import base64
import json
import sys
import time
import urllib.request
from compression import zstd
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
FULL_DIR = ROOT / ".local" / "samples"
DOC_DIR = ROOT / "docs" / "demos" / "samples"

RPC = {
    "solana": "https://api.mainnet-beta.solana.com",
    "base": "https://mainnet.base.org",
    "bsc": "https://bsc-dataseed.bnbchain.org",
}

PUMP = "6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P"
PUMP_SWAP = "pAMMBay6oceH9fJKBRHGP5D4bD4sWpmSwMn52FMfXEA"
VOTE = "Vote111111111111111111111111111111111111111"
WSOL = "So11111111111111111111111111111111111111112"
USDC_SOL = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

BASE_POOL_MANAGER = "0x498581ff718922c3f8e6a244956af099b2652b2b"
BASE_CLANKER_V4 = "0xe85a59c628f7d27878aceb4bf3b35733630083a9"
BASE_WETH_USDC_V3 = "0xd0b53d9277642d899df5c87a3966a349a798f224"
BSC_TOKEN_MANAGER2 = "0x5c952063c7fc8610ffdb798152d69f0b9550762b"
BSC_WBNB_USDT_V2 = "0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae"

# keccak256 of the event signatures (verified with `cast keccak`).
TOPIC_V4_SWAP = "0x40e9cecb9f5f1f1c5b9c97dec2917b7ee92e57ba5563708daca94dd84ad7112f"
TOPIC_V3_SWAP = "0xc42079f94a6350d7e6235f29174924f928cc2ac818eb64fed8004e115fbcca67"
TOPIC_V2_SWAP = "0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822"
TOPIC_FOUR_CREATE = "0x396d5e902b675b032348d3d2e9517ee8f0c4a926603fbc075d3d282ff00cad20"
TOPIC_FOUR_PURCHASE = "0x7db52723a3b2cdd6164364b3b766e65e540d7be48ffa89582956d8eaebe62942"
TOPIC_FOUR_SALE = "0x0a5575b3648bae2210cee56bf33254cc1ddfbc7bf637c0af2ac18b14fb1bae19"


def has_log(receipt, address, topic):
    return any(lg["address"].lower() == address and lg["topics"] and lg["topics"][0] == topic
               for lg in receipt["logs"])


def count_logs(receipt, topic):
    return sum(1 for lg in receipt["logs"] if lg["topics"] and lg["topics"][0] == topic)


EVM_TARGETS = {
    "base": {
        # A plain v4 swap: one v4 Swap, no v3/v2 hops, few logs.
        "uniswap-v4-swap": lambda r: count_logs(r, TOPIC_V4_SWAP) == 1 and count_logs(r, TOPIC_V3_SWAP) == 0
        and count_logs(r, TOPIC_V2_SWAP) == 0 and len(r["logs"]) <= 10,
        # Aggregator route that crosses a v3 pool and a v4 pool in one transaction.
        "aggregator-two-hop": lambda r: count_logs(r, TOPIC_V4_SWAP) >= 1 and count_logs(r, TOPIC_V3_SWAP) >= 1,
        "weth-usdc-v3-swap": lambda r: has_log(r, BASE_WETH_USDC_V3, TOPIC_V3_SWAP) and len(r["logs"]) <= 8,
    },
    "bsc": {
        "fourmeme-create": lambda r: has_log(r, BSC_TOKEN_MANAGER2, TOPIC_FOUR_CREATE),
        "fourmeme-buy": lambda r: has_log(r, BSC_TOKEN_MANAGER2, TOPIC_FOUR_PURCHASE)
        and not has_log(r, BSC_TOKEN_MANAGER2, TOPIC_FOUR_CREATE) and len(r["logs"]) <= 12,
        "fourmeme-sell": lambda r: has_log(r, BSC_TOKEN_MANAGER2, TOPIC_FOUR_SALE) and len(r["logs"]) <= 12,
        "wbnb-usdt-v2-swap": lambda r: has_log(r, BSC_WBNB_USDT_V2, TOPIC_V2_SWAP) and len(r["logs"]) <= 8,
    },
}

B58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"


def b58encode(raw: bytes) -> str:
    n = int.from_bytes(raw, "big")
    out = ""
    while n:
        n, r = divmod(n, 58)
        out = B58[r] + out
    pad = len(raw) - len(raw.lstrip(b"\0"))
    return "1" * pad + out


def b58decode(s: str) -> bytes:
    n = 0
    for c in s:
        n = n * 58 + B58.index(c)
    raw = n.to_bytes((n.bit_length() + 7) // 8, "big")
    pad = len(s) - len(s.lstrip("1"))
    return b"\0" * pad + raw


def rpc(chain, method, params, retries=5):
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode()
    for attempt in range(retries):
        req = urllib.request.Request(RPC[chain], body, {
            "content-type": "application/json", "user-agent": "meme-radar-samples/0.1"})
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                raw = resp.read()
        except Exception as exc:  # network errors and HTTP 429
            wait = 2 ** attempt
            print(f"  {method} failed ({exc}); retry in {wait}s", file=sys.stderr)
            time.sleep(wait)
            continue
        data = json.loads(raw)
        if "error" in data:
            raise RuntimeError(f"{method}: {data['error']}")
        return data["result"], len(raw)
    raise RuntimeError(f"{method}: retries exhausted")


def save(path: Path, obj):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(obj, indent=2, ensure_ascii=False) + "\n")


def size_report(raw_json: bytes):
    return {
        "json_bytes": len(raw_json),
        "zstd3_bytes": len(zstd.compress(raw_json, level=3)),
        "zstd19_bytes": len(zstd.compress(raw_json, level=19)),
    }


# ---------------------------------------------------------------- Solana

def sol_keys(tx):
    msg = tx["transaction"]["message"]
    keys = list(msg["accountKeys"])
    loaded = tx["meta"].get("loadedAddresses") or {}
    return keys + loaded.get("writable", []) + loaded.get("readonly", [])


def sol_programs(tx):
    keys = sol_keys(tx)
    progs = {keys[ix["programIdIndex"]] for ix in tx["transaction"]["message"]["instructions"]}
    for inner in tx["meta"].get("innerInstructions") or []:
        for ix in inner["instructions"]:
            progs.add(keys[ix["programIdIndex"]])
    return progs


def sol_token_deltas(tx):
    """Per (owner, mint) token balance change in UI units."""
    pre = {(b["accountIndex"]): b for b in tx["meta"].get("preTokenBalances") or []}
    post = {(b["accountIndex"]): b for b in tx["meta"].get("postTokenBalances") or []}
    out = {}
    for idx in set(pre) | set(post):
        b = post.get(idx) or pre.get(idx)
        before = int(pre[idx]["uiTokenAmount"]["amount"]) if idx in pre else 0
        after = int(post[idx]["uiTokenAmount"]["amount"]) if idx in post else 0
        key = (b.get("owner"), b["mint"], b["uiTokenAmount"]["decimals"])
        out[key] = out.get(key, 0) + after - before
    return {k: v for k, v in out.items() if v}


def fetch_solana():
    tip, _ = rpc("solana", "getSlot", [{"commitment": "confirmed"}])
    print(f"solana tip slot {tip}")
    slots, _ = rpc("solana", "getBlocks", [tip - 60, tip - 40, {"commitment": "confirmed"}])
    skipped = [s for s in range(tip - 60, tip - 39) if s not in slots]
    measured = []
    pump_tx = pump_swap_tx = sol_usdc_tx = None
    block_header = None
    for slot in slots[:3]:
        block, nbytes = rpc("solana", "getBlock", [slot, {
            "encoding": "json", "maxSupportedTransactionVersion": 1,
            "transactionDetails": "full", "rewards": False, "commitment": "confirmed"}])
        raw = json.dumps(block, separators=(",", ":")).encode()
        (FULL_DIR / "solana").mkdir(parents=True, exist_ok=True)
        (FULL_DIR / "solana" / f"block-{slot}.json.zst").write_bytes(zstd.compress(raw, level=3))
        txs = block["transactions"]
        votes = sum(1 for t in txs if VOTE in sol_programs(t))
        rep = {"slot": slot, "tx_count": len(txs), "vote_tx_count": votes,
               "failed_tx_count": sum(1 for t in txs if t["meta"]["err"]), **size_report(raw)}
        non_vote = [t for t in txs if VOTE not in sol_programs(t)]
        rep["non_vote_" + "zstd3_bytes"] = len(zstd.compress(
            json.dumps(non_vote, separators=(",", ":")).encode(), level=3))
        measured.append(rep)
        print(" ", rep)
        if block_header is None:
            block_header = {k: v for k, v in block.items() if k != "transactions"}
            block_header["slot"] = slot
        for t in txs:
            if t["meta"]["err"]:
                continue
            progs = sol_programs(t)
            if pump_tx is None and PUMP in progs and PUMP_SWAP not in progs:
                deltas = sol_token_deltas(t)
                if any(m != WSOL for (_, m, _) in deltas):
                    pump_tx = (slot, block.get("blockTime"), t)
            if pump_swap_tx is None and PUMP_SWAP in progs:
                pump_swap_tx = (slot, block.get("blockTime"), t)
            if sol_usdc_tx is None:
                mints = {m for (_, m, _) in sol_token_deltas(t)}
                if mints == {WSOL, USDC_SOL}:
                    sol_usdc_tx = (slot, block.get("blockTime"), t)

    out = DOC_DIR / "solana"
    save(out / "block-header.json", block_header)
    save(out / "block-size.json", {"tip_slot": tip, "skipped_slots_in_range": skipped, "blocks": measured})
    for name, item in (("pump-trade", pump_tx), ("pumpswap-trade", pump_swap_tx), ("sol-usdc-swap", sol_usdc_tx)):
        if item is None:
            print(f"  no {name} found")
            continue
        slot, block_time, tx = item
        save(out / f"{name}.json", {"slot": slot, "blockTime": block_time, "transaction": tx,
                                    "token_deltas_by_owner": [
                                        {"owner": o, "mint": m, "decimals": d, "delta_atomic": str(v)}
                                        for (o, m, d), v in sol_token_deltas(tx).items()]})
        print(f"  saved {name} {tx['transaction']['signatures'][0]}")


# ---------------------------------------------------------------- EVM

def fetch_evm(chain, scan_blocks, interesting):
    tip_hex, _ = rpc(chain, "eth_blockNumber", [])
    tip = int(tip_hex, 16)
    print(f"{chain} tip {tip}")
    measured = []
    found = {}
    header = None
    for i in range(scan_blocks):
        n = tip - 15 - i
        block, _ = rpc(chain, "eth_getBlockByNumber", [hex(n), True])
        receipts, _ = rpc(chain, "eth_getBlockReceipts", [hex(n)])
        raw = json.dumps({"block": block, "receipts": receipts}, separators=(",", ":")).encode()
        if i < 3:
            (FULL_DIR / chain).mkdir(parents=True, exist_ok=True)
            (FULL_DIR / chain / f"block-{n}.json.zst").write_bytes(zstd.compress(raw, level=3))
            rep = {"number": n, "tx_count": len(block["transactions"]),
                   "log_count": sum(len(r["logs"]) for r in receipts), **size_report(raw)}
            measured.append(rep)
            print(" ", rep)
        if header is None:
            header = {k: v for k, v in block.items() if k != "transactions"}
            header["transaction_count"] = len(block["transactions"])
        by_hash = {t["hash"]: t for t in block["transactions"]}
        for r in receipts:
            if r["status"] != "0x1":
                continue
            for name, match in interesting.items():
                if name not in found and match(r):
                    found[name] = {"blockNumber": n, "blockTimestamp": int(block["timestamp"], 16),
                                   "transaction": by_hash[r["transactionHash"]], "receipt": r}
                    print(f"  found {name} {r['transactionHash']}")
        if len(found) == len(interesting) and i >= 2:
            break
    out = DOC_DIR / chain
    save(out / "block-header.json", header)
    save(out / "block-size.json", {"tip": tip, "blocks": measured})
    for name, item in found.items():
        save(out / f"{name}.json", item)


def main():
    targets = sys.argv[1:] or ["solana", "base", "bsc"]
    if "solana" in targets:
        fetch_solana()
    if "base" in targets:
        fetch_evm("base", 60, EVM_TARGETS["base"])
    if "bsc" in targets:
        fetch_evm("bsc", 200, EVM_TARGETS["bsc"])


if __name__ == "__main__":
    main()
