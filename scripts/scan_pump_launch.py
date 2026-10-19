#!/usr/bin/env python3
"""Find a fresh Pump launch with many early buyers and probe the buyers' funding.

Writes docs/demos/samples/solana/pump-launch-early-trades.json for docs/demos/07-labels.md.
Read-only public RPC, no API keys. A scan of 40 slots takes about 3 minutes.

Usage: python3 scripts/scan_pump_launch.py [slots]
"""

import base64
import json
import struct
import sys
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from fetch_samples import DOC_DIR, PUMP, b58encode, rpc, save  # noqa: E402

TRADE_EVENT = "bddb7fd34ee661ee"
CREATE_LOGS = ("Program log: Instruction: Create", "Program log: Instruction: CreateV2")
SNIPE_SLOTS = 10


def get_block(slot):
    try:
        block, _ = rpc("solana", "getBlock", [slot, {
            "encoding": "json", "maxSupportedTransactionVersion": 1, "transactionDetails": "full",
            "rewards": False, "commitment": "confirmed"}])
        return slot, block
    except RuntimeError as exc:
        print(f"  slot {slot}: {exc}", file=sys.stderr)
        return slot, None


def scan(slots):
    with ThreadPoolExecutor(3) as pool:
        blocks = dict(pool.map(get_block, slots))
    creates, trades = {}, []
    for slot in slots:
        block = blocks[slot]
        if block is None:
            continue
        for idx, tx in enumerate(block["transactions"]):
            meta = tx["meta"]
            if meta is None or meta["err"] is not None:
                continue
            logs = meta.get("logMessages") or []
            if not any(PUMP in line for line in logs):
                continue
            sig = tx["transaction"]["signatures"][0]
            signer = tx["transaction"]["message"]["accountKeys"][0]
            is_create = any(line in CREATE_LOGS for line in logs)
            for line in logs:
                if not line.startswith("Program data: "):
                    continue
                raw = base64.b64decode(line[len("Program data: "):])
                if raw[:8].hex() != TRADE_EVENT:
                    continue
                sol, tok = struct.unpack_from("<QQ", raw, 40)
                trades.append({"slot": slot, "idx": idx, "sig": sig, "mint": b58encode(raw[8:40]),
                               "buy": bool(raw[56]), "user": b58encode(raw[57:89]), "signer": signer,
                               "sol": sol / 1e9, "tok": tok / 1e6, "create_tx": is_create})
            if is_create:
                mints = [b["mint"] for b in meta.get("postTokenBalances", []) if b["mint"].endswith("pump")]
                if mints:
                    creates.setdefault(mints[0], {"slot": slot, "idx": idx, "signer": signer, "sig": sig})
    failed = [s for s, b in blocks.items() if b is None]
    return creates, trades, failed


def funding(wallet, before_sig, depth=3):
    sigs, _ = rpc("solana", "getSignaturesForAddress", [wallet, {"limit": depth, "before": before_sig}])
    out = []
    for s in sigs:
        tx, _ = rpc("solana", "getTransaction", [s["signature"], {
            "encoding": "jsonParsed", "maxSupportedTransactionVersion": 1}])
        ixs = tx["transaction"]["message"]["instructions"] + [
            ix for group in (tx["meta"].get("innerInstructions") or []) for ix in group["instructions"]]
        sol_in = [(ix["parsed"]["info"]["source"], ix["parsed"]["info"]["lamports"] / 1e9) for ix in ixs
                  if ix.get("program") == "system" and isinstance(ix.get("parsed"), dict)
                  and ix["parsed"].get("type") == "transfer" and ix["parsed"]["info"].get("destination") == wallet]
        out.append({"sig": s["signature"], "slot": s["slot"], "blockTime": s["blockTime"],
                    "signer": tx["transaction"]["message"]["accountKeys"][0]["pubkey"], "sol_in": sol_in})
    return out


def main():
    n = int(sys.argv[1]) if len(sys.argv) > 1 else 40
    tip, _ = rpc("solana", "getSlot", [{"commitment": "confirmed"}])
    slots = list(range(tip - 100, tip - 100 + n))
    creates, trades, failed = scan(slots)
    ranked = sorted(creates.items(), key=lambda kv: -len({
        t["user"] for t in trades
        if t["mint"] == kv[0] and t["buy"] and t["slot"] <= kv[1]["slot"] + SNIPE_SLOTS}))
    if not ranked:
        sys.exit("no Pump launch in range")
    mint, create = ranked[0]
    early = sorted((t for t in trades if t["mint"] == mint), key=lambda t: (t["slot"], t["idx"]))
    same_slot = [t for t in early if t["slot"] == create["slot"] and t["buy"]]
    result = {
        "source": f"api.mainnet-beta.solana.com, getBlock slots {slots[0]}..{slots[-1]}"
                  + (f" ({len(failed)} slots failed after retries: {', '.join(map(str, failed))})" if failed else ""),
        "mint": mint,
        "create": create,
        "trades": [{k: v for k, v in t.items() if k != "mint"} for t in early],
        "funding_probe": {
            "method": "getSignaturesForAddress(limit=3, before=buy) then getTransaction; "
                      "sol_in = system transfers into the wallet",
            "results": {t["user"]: funding(t["user"], t["sig"]) for t in same_slot},
        },
    }
    save(DOC_DIR / "solana" / "pump-launch-early-trades.json", result)
    print(f"{mint}: {len(early)} trades, {len(same_slot)} same-slot buyers, {len(failed)} failed slots")


if __name__ == "__main__":
    main()
