#!/usr/bin/env python3
"""Decode the trimmed samples in docs/demos/samples/ into docs/demos/samples/decoded.json.

Reads only local sample files, except token metadata (eth_call / getTokenSupply)
which is fetched read-only from the same public endpoints.
"""

import base64
import json
import struct
import sys
from decimal import Decimal, getcontext
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from fetch_samples import DOC_DIR, b58encode, rpc  # noqa: E402

getcontext().prec = 40


def load(chain, name):
    return json.loads((DOC_DIR / chain / f"{name}.json").read_text())


def words(data_hex):
    raw = bytes.fromhex(data_hex[2:])
    return [raw[i:i + 32] for i in range(0, len(raw), 32)]


def uint(w):
    return int.from_bytes(w, "big")


def sint(w):
    v = int.from_bytes(w, "big")
    return v - (1 << 256) if v >> 255 else v


def addr(w):
    return "0x" + w[-20:].hex()


def abi_string(data_hex, offset):
    raw = bytes.fromhex(data_hex[2:])
    n = int.from_bytes(raw[offset:offset + 32], "big")
    return raw[offset + 32:offset + 32 + n].decode()


def units(v, decimals):
    return str(Decimal(v) / (Decimal(10) ** decimals))


def ratio(a, b):
    return str((Decimal(a) / Decimal(b)).normalize())


def erc20_meta(chain, token):
    def call(selector):
        res, _ = rpc(chain, "eth_call", [{"to": token, "data": selector}, "latest"])
        return res

    meta = {"address": token}
    for field, sel in (("name", "0x06fdde03"), ("symbol", "0x95d89b41")):
        try:
            meta[field] = abi_string(call(sel), 32)
        except Exception as exc:  # non-standard tokens
            meta[field] = f"<{exc}>"
    meta["decimals"] = int(call("0x313ce567"), 16)
    meta["totalSupply_atomic"] = str(int(call("0x18160ddd"), 16))
    return meta


def transfers(receipt):
    topic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"  # Transfer(address,address,uint256)
    out = []
    for lg in receipt["logs"]:
        if lg["topics"][0] == topic and len(lg["topics"]) == 3:
            out.append({"logIndex": int(lg["logIndex"], 16), "token": lg["address"].lower(),
                        "from": "0x" + lg["topics"][1][-40:], "to": "0x" + lg["topics"][2][-40:],
                        "value_atomic": str(int(lg["data"], 16))})
    return out


def find_log(receipt, topic_prefix, address=None):
    for lg in receipt["logs"]:
        if lg["topics"][0].startswith(topic_prefix) and (address is None or lg["address"].lower() == address):
            return lg
    raise KeyError(topic_prefix)


def solana():
    sol_usdc = load("solana", "sol-usdc-swap")
    d = {x["mint"][:4]: int(x["delta_atomic"]) for x in sol_usdc["token_deltas_by_owner"]
         if x["owner"] == sol_usdc["transaction"]["transaction"]["message"]["accountKeys"][0]}
    usdc_paid, sol_got = -d["EPjF"], d["So11"]
    sol_usd = Decimal(usdc_paid) / 10**6 / (Decimal(sol_got) / 10**9)
    out = {"sol_usdc_swap": {
        "signature": sol_usdc["transaction"]["transaction"]["signatures"][0], "slot": sol_usdc["slot"],
        "usdc_paid": units(usdc_paid, 6), "sol_received": units(sol_got, 9), "sol_usd": str(round(sol_usd, 6))}}

    pump = load("solana", "pump-trade")
    log = next(lg for lg in pump["transaction"]["meta"]["logMessages"] if lg.startswith("Program data: "))
    raw = base64.b64decode(log.split(": ", 1)[1])
    o = 8
    ev = {"discriminator": raw[:8].hex(), "mint": b58encode(raw[o:o + 32])}
    o += 32
    ev["sol_amount"], ev["token_amount"] = struct.unpack_from("<QQ", raw, o)
    o += 16
    ev["is_buy"] = bool(raw[o])
    o += 1
    ev["user"] = b58encode(raw[o:o + 32])
    o += 32
    (ev["timestamp"], ev["virtual_sol_reserves"], ev["virtual_token_reserves"],
     ev["real_sol_reserves"], ev["real_token_reserves"]) = struct.unpack_from("<qQQQQ", raw, o)
    o += 40
    ev["fee_recipient"] = b58encode(raw[o:o + 32])
    o += 32
    ev["fee_basis_points"], ev["fee"] = struct.unpack_from("<QQ", raw, o)
    o += 16
    ev["creator"] = b58encode(raw[o:o + 32])
    o += 32
    ev["creator_fee_basis_points"], ev["creator_fee"] = struct.unpack_from("<QQ", raw, o)
    supply, _ = rpc("solana", "getTokenSupply", [ev["mint"]])
    tx = pump["transaction"]
    keys = tx["transaction"]["message"]["accountKeys"]
    lamport_deltas = {keys[i]: b - a for i, (a, b) in enumerate(zip(tx["meta"]["preBalances"], tx["meta"]["postBalances"]))
                      if i < len(keys) and a != b}
    avg_price_sol = Decimal(ev["sol_amount"]) / 10**9 / (Decimal(ev["token_amount"]) / 10**6)
    spot_sol = (Decimal(ev["virtual_sol_reserves"]) / 10**9) / (Decimal(ev["virtual_token_reserves"]) / 10**6)
    total_supply = Decimal(supply["value"]["amount"]) / 10**supply["value"]["decimals"]
    out["pump_trade"] = {
        "signature": tx["transaction"]["signatures"][0], "slot": pump["slot"], "version": tx.get("version"),
        "top_level_programs": [keys[ix["programIdIndex"]] for ix in tx["transaction"]["message"]["instructions"]],
        "network_fee_lamports": tx["meta"]["fee"], "lamport_deltas": lamport_deltas, "trade_event": ev,
        "token_supply": supply["value"],
        "avg_price_sol": str(avg_price_sol), "avg_price_usd": str(avg_price_sol * sol_usd),
        "spot_price_sol_after": str(spot_sol), "spot_price_usd_after": str(spot_sol * sol_usd),
        "market_cap_usd_after": str(round(spot_sol * sol_usd * total_supply, 2)),
        "curve_real_tokens_left": units(ev["real_token_reserves"], 6),
        "curve_progress_vs_793_1m": str(round(1 - Decimal(ev["real_token_reserves"]) / Decimal(793_100_000 * 10**6), 4)),
        "user_trade_cost_lamports": ev["sol_amount"] + ev["fee"] + ev["creator_fee"],
    }

    ps = load("solana", "pumpswap-trade")
    log = next(lg for lg in ps["transaction"]["meta"]["logMessages"] if lg.startswith("Program data: "))
    raw = base64.b64decode(log.split(": ", 1)[1])
    names = ["timestamp", "base_amount_in", "min_quote_amount_out", "user_base_token_reserves",
             "user_quote_token_reserves", "pool_base_token_reserves", "pool_quote_token_reserves",
             "quote_amount_out", "lp_fee_basis_points", "lp_fee", "protocol_fee_basis_points", "protocol_fee",
             "quote_amount_out_without_lp_fee", "user_quote_amount_out"]
    vals = struct.unpack_from("<q" + "Q" * 13, raw, 8)
    ev = dict(zip(names, vals))
    ev["discriminator"] = raw[:8].hex()
    mint = next(x["mint"] for x in ps["token_deltas_by_owner"] if x["mint"] != "So11111111111111111111111111111111111111112")
    supply, _ = rpc("solana", "getTokenSupply", [mint])
    pool_sol = Decimal(ev["pool_base_token_reserves"]) / 10**9
    pool_tok = Decimal(ev["pool_quote_token_reserves"]) / 10**6
    spot = pool_sol / pool_tok
    sol_in = Decimal(ev["base_amount_in"]) / 10**9
    post_sol = pool_sol + sol_in
    post_tok = pool_tok - (Decimal(ev["quote_amount_out"]) - Decimal(ev["lp_fee"])) / 10**6
    out["pumpswap_check"] = {
        "constant_product_quote_out": units(int(pool_tok * sol_in / (pool_sol + sol_in) * 10**6), 6),
        "event_quote_out": units(ev["quote_amount_out"], 6),
        "post_trade_price_usd": str(post_sol / post_tok * sol_usd),
        "post_trade_liquidity_usd": str(round(post_sol * 2 * sol_usd, 2)),
    }
    out["pumpswap_trade"] = {
        "signature": ps["transaction"]["transaction"]["signatures"][0], "slot": ps["slot"],
        "instruction_name": "Sell", "token_mint": mint, "sell_event": ev, "token_supply": supply["value"],
        "note": "pool base = WSOL, quote = token; instruction 'Sell' sells WSOL, so from the token's view this is a BUY",
        "spot_price_sol": str(spot), "spot_price_usd": str(spot * sol_usd),
        "liquidity_usd": str(round(pool_sol * 2 * sol_usd, 2)),
        "market_cap_usd": str(round(spot * sol_usd * Decimal(supply["value"]["amount"]) / 10**supply["value"]["decimals"], 2)),
    }
    return out, sol_usd


def base():
    v3 = load("base", "weth-usdc-v3-swap")
    lg = find_log(v3["receipt"], "0xc42079f9")
    w = words(lg["data"])
    a0, a1 = sint(w[0]), sint(w[1])
    eth_usd = Decimal(abs(a1)) / 10**6 / (Decimal(abs(a0)) / 10**18)
    out = {"weth_usdc_v3_swap": {"tx": v3["transaction"]["hash"], "block": v3["blockNumber"],
                                 "amount0_weth_atomic": str(a0), "amount1_usdc_atomic": str(a1),
                                 "eth_usd": str(round(eth_usd, 6))}}

    v4 = load("base", "uniswap-v4-swap")
    r, t = v4["receipt"], v4["transaction"]
    lg = find_log(r, "0x40e9cecb")
    w = words(lg["data"])
    trs = transfers(r)
    token = next(x["token"] for x in trs if x["token"] != "0x4200000000000000000000000000000000000006")
    meta = erc20_meta("base", token)
    tok_in = next(int(x["value_atomic"]) for x in trs if x["token"] == token)
    weth_out = next(int(x["value_atomic"]) for x in trs if x["token"].startswith("0x4200"))
    price_eth = Decimal(weth_out) / 10**18 / (Decimal(tok_in) / 10**meta["decimals"])
    fee_wei = int(r["gasUsed"], 16) * int(r["effectiveGasPrice"], 16)
    out["uniswap_v4_swap"] = {
        "tx": t["hash"], "block": v4["blockNumber"], "tx_from": t["from"], "tx_to_router": t["to"],
        "swap_event": {"poolId": lg["topics"][1], "sender": "0x" + lg["topics"][2][-40:],
                       "amount0": str(sint(w[0])), "amount1": str(sint(w[1])),
                       "sqrtPriceX96": str(uint(w[2])), "liquidity": str(uint(w[3])),
                       "tick": sint(w[4]), "fee": uint(w[5])},
        "erc20_transfers": trs, "token": meta,
        "native_eth_to_user_visible_in_logs": False,
        "price_eth": str(price_eth), "price_usd": str(price_eth * eth_usd),
        "l2_execution_fee_wei": str(fee_wei), "l1_fee_wei": str(int(r["l1Fee"], 16)),
    }
    agg = load("base", "aggregator-two-hop")
    r, t = agg["receipt"], agg["transaction"]
    trs = transfers(r)
    metas = {tok: erc20_meta("base", tok) for tok in {x["token"] for x in trs} if not tok.startswith("0x4200")}
    final = trs[-1]["token"]
    final_dec = metas[final]["decimals"]
    final_out = [x for x in trs if x["token"] == final]
    gross = sum(int(x["value_atomic"]) for x in final_out)
    to_user = sum(int(x["value_atomic"]) for x in final_out if x["to"] == t["from"])
    eth_in = int(t["value"], 16)
    weth_swapped = next(int(x["value_atomic"]) for x in trs if x["token"].startswith("0x4200"))
    out["aggregator_two_hop"] = {
        "tx": t["hash"], "block": agg["blockNumber"], "tx_from": t["from"], "tx_to_aggregator": t["to"],
        "eth_in": units(eth_in, 18), "weth_swapped": units(weth_swapped, 18),
        "aggregator_fee_eth": units(eth_in - weth_swapped, 18),
        "swap_events": [x["topics"][0][:10] for x in r["logs"]
                        if x["topics"][0][:10] in ("0x40e9cecb", "0xc42079f9", "0xd78ad95f")],
        "tokens": metas, "erc20_transfers": trs,
        "final_token_gross_out": units(gross, final_dec), "final_token_to_user": units(to_user, final_dec),
        "final_token_fee_splits": {x["to"]: ratio(int(x["value_atomic"]), gross) for x in final_out
                                   if x["to"] != t["from"]},
        "user_price_usd": str(Decimal(eth_in) / Decimal(to_user) * Decimal(10) ** (final_dec - 18) * eth_usd),
        "gas_used": int(r["gasUsed"], 16), "log_count": len(r["logs"]),
    }
    return out, eth_usd


def bsc():
    v2 = load("bsc", "wbnb-usdt-v2-swap")
    r = v2["receipt"]
    pair = "0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae"
    lg = find_log(r, "0xd78ad95f", pair)
    a0in, a1in, a0out, a1out = (uint(x) for x in words(lg["data"]))
    bnb_usd = Decimal(a0in) / Decimal(a1out)  # token0 = USDT(18), token1 = WBNB(18)
    second = [x for x in r["logs"] if x["topics"][0].startswith("0xd78ad95f")]
    trs = transfers(r)
    out_token = trs[-1]["token"]
    out = {"wbnb_usdt_v2_multihop": {
        "tx": v2["transaction"]["hash"], "block": v2["blockNumber"], "tx_from": v2["transaction"]["from"],
        "router": v2["transaction"]["to"], "hops": len(second), "erc20_transfers": trs,
        "first_hop": {"usdt_in": units(a0in, 18), "wbnb_out": units(a1out, 18)},
        "bnb_usd": str(round(bnb_usd, 6)), "final_token": erc20_meta("bsc", out_token)}}

    def four_purchase(lg):
        w = words(lg["data"])
        return {"token": addr(w[0]), "account": addr(w[1]), "price": str(uint(w[2])), "amount": str(uint(w[3])),
                "cost": str(uint(w[4])), "fee": str(uint(w[5])), "offers": str(uint(w[6])), "funds": str(uint(w[7]))}

    buy = load("bsc", "fourmeme-buy")
    ev = four_purchase(find_log(buy["receipt"], "0x7db52723"))
    meta = erc20_meta("bsc", ev["token"])
    trs = transfers(buy["receipt"])
    usdt_paid = next(int(x["value_atomic"]) for x in trs if x["from"] == buy["transaction"]["from"])
    out["fourmeme_buy"] = {
        "tx": buy["transaction"]["hash"], "block": buy["blockNumber"], "tx_from": buy["transaction"]["from"],
        "token": meta, "token_purchase_event": ev, "erc20_transfers": trs,
        "quote_asset": "USDT 0x55d398326f99059ff775485246999027b3197955",
        "usdt_paid_by_user": units(usdt_paid, 18),
        "avg_price_usd": ratio(Decimal(ev["cost"]) / 10**18, Decimal(ev["amount"]) / 10**18),
        "last_price_usd": units(int(ev["price"]), 18),
        "market_cap_usd": str(round(Decimal(ev["price"]) / 10**18 * Decimal(meta["totalSupply_atomic"]) / 10**18, 2)),
    }

    cr = load("bsc", "fourmeme-create")
    lg = find_log(cr["receipt"], "0x396d5e90")
    w = words(lg["data"])
    create = {"creator": addr(w[0]), "token": addr(w[1]), "requestId": str(uint(w[2])),
              "name": abi_string(lg["data"], uint(w[3])), "symbol": abi_string(lg["data"], uint(w[4])),
              "totalSupply": str(uint(w[5])), "launchTime": uint(w[6]), "launchFee": str(uint(w[7]))}
    out["fourmeme_create"] = {
        "tx": cr["transaction"]["hash"], "block": cr["blockNumber"], "blockTimestamp": cr["blockTimestamp"],
        "tx_from": cr["transaction"]["from"], "tx_value_wei": str(int(cr["transaction"]["value"], 16)),
        "token_create_event": create, "dev_buy_event": four_purchase(find_log(cr["receipt"], "0x7db52723")),
    }
    dev = out["fourmeme_create"]["dev_buy_event"]
    dev_price_bnb = Decimal(dev["price"]) / 10**18
    out["fourmeme_create"]["dev_price_usd"] = str(dev_price_bnb * bnb_usd)
    out["fourmeme_create"]["dev_market_cap_usd"] = str(round(dev_price_bnb * bnb_usd * Decimal(create["totalSupply"]) / 10**18, 2))
    return out, bnb_usd


def main():
    sol, sol_usd = solana()
    b, eth_usd = base()
    s, bnb_usd = bsc()
    result = {"quote_asset_usd": {"SOL": str(round(sol_usd, 4)), "ETH": str(round(eth_usd, 4)),
                                  "BNB": str(round(bnb_usd, 4))},
              "solana": sol, "base": b, "bsc": s}
    (DOC_DIR / "decoded.json").write_text(json.dumps(result, indent=2, ensure_ascii=False) + "\n")
    print(json.dumps(result["quote_asset_usd"]))


if __name__ == "__main__":
    main()
