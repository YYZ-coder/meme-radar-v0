// Mock implementation of api/openapi.yaml for frontend development and browser smoke tests.
// Served by src/app/api/v1/[...path]/route.ts when RADAR_API_ORIGIN is not configured.
import { timingSafeEqual } from "node:crypto";
import type {
  Activity,
  CandleInterval,
  Chain,
  Holder,
  LiquidityEvent,
  Pool,
  SearchToken,
  WalletRef,
  Window,
  WindowSummary,
} from "@/lib/api/types";
import { adminState, bumpVersion, health, isMember, memberId } from "./admin";
import { ImportFileError, MAX_BYTES, readRecords, validateRecords } from "./csv";
import {
  conv,
  dexes,
  findToken,
  inflowRow,
  metrics,
  nativeSymbol,
  nativeUsd,
  platforms,
  priceAt,
  tokenRef,
  tokens,
  windowSeconds,
  type MockToken,
} from "./data";
import { HttpError, listVersionRef, newSnapshot, page } from "./inflow";
import { dec, evmAddress, hash, rng, solanaAddress, txHash, unit } from "./random";

const json = (body: unknown, status = 200, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), {
    status,
    headers: {
      "content-type": "application/json",
      "cache-control": "no-store",
      "x-radar-mock": "1",
      ...headers,
    },
  });

const error = (status: number, code: string, message: string, field: string | null = null) =>
  json({ error: { code, message, field } }, status);

const chainList: Chain[] = ["solana", "base", "bsc"];
const L_MS = 30 * 86400_000; // history lower bound L is 30 days before anchor A (D73)

function requireToken(chain: string, address: string): MockToken {
  if (!chainList.includes(chain as Chain))
    throw new HttpError(400, "invalid_param", "chain", "chain");
  const token = findToken(chain, address);
  if (!token) throw new HttpError(404, "not_found", "token not found");
  return token;
}

function int(params: URLSearchParams, name: string, fallback: number, max: number) {
  const raw = params.get(name);
  if (raw === null || raw === "") return fallback;
  const n = Number(raw);
  if (!Number.isInteger(n) || n < 0) throw new HttpError(400, "invalid_param", name, name);
  return Math.min(n, max);
}

function windowParam(params: URLSearchParams): Window {
  const w = params.get("window") ?? "1h";
  if (!(w in windowSeconds)) throw new HttpError(400, "invalid_param", "window", "window");
  return w as Window;
}

function cursorDecode(cursor: string | null): { anchor: number; offset: number } | null {
  if (!cursor) return null;
  const [anchor, offset] = Buffer.from(cursor, "base64url").toString().split(":").map(Number);
  if (!Number.isFinite(anchor) || !Number.isFinite(offset)) {
    throw new HttpError(400, "invalid_param", "cursor", "cursor");
  }
  return { anchor, offset };
}
const cursorEncode = (anchor: number, offset: number) =>
  Buffer.from(`${anchor}:${offset}`).toString("base64url");

// ---------------------------------------------------------------- wallets

const smWallets = (chain: Chain) =>
  adminState.members.filter((m) => m.chain === chain && !m.excluded);

/** The i-th distinct smart-money wallet for a token (holder lists must not repeat). */
function smWalletAt(token: MockToken, i: number): WalletRef | null {
  const pool = smWallets(token.chain);
  if (i >= pool.length) return null;
  const m = pool[(token.seed + i) % pool.length];
  return {
    address: m.address,
    labels: m.tags.includes("kol") ? ["smart_money", "kol"] : ["smart_money"],
    display_name: m.label,
  };
}

function walletFor(token: MockToken, key: string, smChance: number): WalletRef {
  const r = rng(hash(`${token.address}:${key}`));
  const pool = smWallets(token.chain);
  if (pool.length && r() < smChance) {
    const m = pool[Math.floor(r() * pool.length)];
    return {
      address: m.address,
      labels: m.tags.includes("kol") ? ["smart_money", "kol"] : ["smart_money"],
      display_name: m.label,
    };
  }
  const n = Math.floor(r() * 400);
  return {
    address: token.chain === "solana" ? solanaAddress(`w${n}`) : evmAddress(`w${n}`),
    labels: [],
    display_name: null,
  };
}

// ---------------------------------------------------------------- token detail

function windowSummary(token: MockToken, w: Window, native: boolean, now: number): WindowSummary {
  const m = metrics(token, w, now);
  const c = (v: number | null) => {
    const x = conv(v, token.chain, native);
    return x === null ? null : dec(x, 8);
  };
  const buyVol = m.volume === null ? null : (m.volume * m.buys) / Math.max(1, m.trades);
  const sellVol = m.volume === null || buyVol === null ? null : m.volume - buyVol;
  return {
    window: w as WindowSummary["window"],
    volume: c(m.volume),
    net_inflow: buyVol === null || sellVol === null ? null : c(buyVol - sellVol),
    traders: m.traders,
    buys: m.buys,
    sells: m.sells,
    buy_volume: c(buyVol),
    sell_volume: c(sellVol),
    price_change_pct:
      m.price === null || m.priceStart === null
        ? null
        : dec(((m.price - m.priceStart) / m.priceStart) * 100, 6),
  };
}

function searchToken(token: MockToken, now: number): SearchToken {
  const m = metrics(token, "24h", now);
  return {
    ...tokenRef(token),
    market_cap_usd: m.marketCap === null ? null : dec(m.marketCap, 8),
    age_seconds: Math.floor((now - token.launchAt) / 1000),
    last_trade_at: new Date(now - Math.floor(unit(token.address) * 90_000)).toISOString(),
  };
}

function coverage(token: MockToken, now: number) {
  const lowerBound = now - L_MS;
  const full = token.launchAt >= lowerBound;
  return {
    status: full ? ("full" as const) : ("partial" as const),
    since: new Date(full ? token.launchAt : lowerBound).toISOString(),
    as_of: new Date(now).toISOString(),
  };
}

function poolsOf(token: MockToken, now: number): Pool[] {
  const m = metrics(token, "24h", now);
  const main = token.dexes[0];
  const list: Pool[] = token.dexes.map((id, i) => {
    const dex = dexes.find((d) => d.id === id)!;
    const share = token.dexes.length > 1 ? (i === 0 ? 0.72 : 0.28) : 1;
    const liq = m.liquidity === null ? null : m.liquidity * share;
    return {
      address:
        token.chain === "solana"
          ? solanaAddress(`pool:${token.address}:${id}`)
          : evmAddress(`pool:${token.address}:${id}`),
      dex: dex.name,
      protocol: dex.protocol,
      quote_symbol: dex.quote,
      liquidity_usd: liq === null ? null : dec(liq, 8),
      reserve_token: liq === null || m.price === null ? null : dec(liq / 2 / m.price, 10),
      reserve_quote: liq === null ? null : dec(liq / 2 / nativeUsd[token.chain], 8),
      created_at: new Date(token.launchAt + i * 3_600_000 * 6).toISOString(),
      status: "active",
      is_main: id === main,
    };
  });
  const platform = platforms.find((p) => p.id === token.platform);
  if (token.graduated && platform?.curve) {
    const curveDex = { pump_fun: "Pump", raydium_launchlab: "LaunchLab", four_meme: "Four.meme" }[
      platform.id
    ];
    list.push({
      address:
        token.chain === "solana"
          ? solanaAddress(`curve:${token.address}`)
          : evmAddress(`curve:${token.address}`),
      dex: curveDex ?? platform.name,
      protocol: `${platform.id}_curve`,
      quote_symbol: nativeSymbol[token.chain],
      liquidity_usd: "0",
      reserve_token: "0",
      reserve_quote: "0",
      created_at: new Date(token.launchAt).toISOString(),
      status: "graduated",
      is_main: false,
    });
  }
  return list;
}

function tokenDetail(token: MockToken, native: boolean, now: number) {
  const m = metrics(token, "24h", now);
  const c = (v: number | null) => {
    const x = conv(v, token.chain, native);
    return x === null ? null : dec(x, 8);
  };
  const pools = poolsOf(token, now);
  const main = pools.find((p) => p.is_main);
  const sameName = tokens
    .filter((t) => t !== token && t.symbol.toLowerCase() === token.symbol.toLowerCase())
    .slice(0, 5)
    .map((t) => searchToken(t, now));
  const ath = token.basePrice === null ? null : token.basePrice * 1.9 * token.supply;
  return {
    token: tokenRef(token),
    currency: native ? nativeSymbol[token.chain] : "USD",
    as_of: new Date(now).toISOString(),
    decimals: token.decimals,
    total_supply: dec(token.supply, 14),
    socials: token.socials,
    creator: {
      address: token.creator,
      holding_pct: token.devHoldingPct === null ? null : dec(token.devHoldingPct, 4),
    },
    main_pool: main
      ? { address: main.address, dex: main.dex, quote_symbol: main.quote_symbol }
      : null,
    price: c(m.price),
    market_cap: c(m.marketCap),
    liquidity: c(m.liquidity),
    volume_24h: c(m.volume),
    price_change_24h_pct:
      m.price === null || m.priceStart === null
        ? null
        : dec(((m.price - m.priceStart) / m.priceStart) * 100, 6),
    holders: token.holders,
    sm_holders: token.smHolders,
    ath_market_cap: c(ath),
    ath_since: new Date(Math.max(token.launchAt, now - L_MS)).toISOString(),
    holder_coverage: coverage(token, now),
    windows: (["5m", "1h", "4h", "24h"] as Window[]).map((w) =>
      windowSummary(token, w, native, now)
    ),
    same_name: sameName,
  };
}

const intervalSeconds: Record<CandleInterval, number> = {
  "1s": 1,
  "30s": 30,
  "1m": 60,
  "5m": 300,
  "1h": 3600,
  "1d": 86400,
};

function candles(token: MockToken, params: URLSearchParams, now: number) {
  const interval = params.get("interval") as CandleInterval;
  if (!(interval in intervalSeconds)) {
    throw new HttpError(
      400,
      "invalid_param",
      "interval must be one of 1s,30s,1m,5m,1h,1d",
      "interval"
    );
  }
  const step = intervalSeconds[interval];
  const metric = params.get("metric") === "market_cap" ? "market_cap" : "price";
  const native = params.get("currency") === "native";
  const limit = int(params, "limit", 300, 1000);
  const toSec = params.get("to")
    ? int(params, "to", 0, Number.MAX_SAFE_INTEGER)
    : Math.floor(now / 1000);
  const end = Math.floor(toSec / step) * step;
  const fromSec = params.get("from")
    ? int(params, "from", 0, Number.MAX_SAFE_INTEGER)
    : end - (limit - 1) * step;
  const start = Math.max(
    Math.ceil(fromSec / step) * step,
    Math.floor(token.launchAt / 1000 / step) * step
  );
  const scale = (p: number) => {
    const v = metric === "market_cap" ? p * token.supply : p;
    return native ? v / nativeUsd[token.chain] : v;
  };
  const items = [];
  for (let t = Math.max(start, end - (limit - 1) * step); t <= end; t += step) {
    const samples = [0, 0.25, 0.5, 0.75, 1].map((f) => priceAt(token, (t + f * step) * 1000));
    if (samples.some((p) => p === null)) continue;
    const ps = samples as number[];
    const noise = 1 + 0.004 * unit(`${token.address}:${interval}:${t}`);
    const vol =
      ((token.tradesPerHour * step) / 3600) *
      (40 + (token.supply * (token.basePrice ?? 0)) / 60_000);
    items.push({
      t,
      o: dec(scale(ps[0]), 8),
      h: dec(scale(Math.max(...ps) * noise), 8),
      l: dec(scale(Math.min(...ps) / noise), 8),
      c: dec(scale(ps[4]), 8),
      v: dec(conv(vol * (0.4 + unit(`v:${token.address}:${t}`)), token.chain, native) ?? 0, 6),
    });
  }
  const platform = platforms.find((p) => p.id === token.platform);
  const pools = poolsOf(token, now);
  const switches =
    token.graduated && platform?.curve
      ? [
          {
            t: Math.floor(token.launchAt / 1000) + 5400,
            from_pool: pools.find((p) => p.status === "graduated")?.address ?? null,
            to_pool: pools.find((p) => p.is_main)?.address ?? "",
            reason: "graduation" as const,
          },
        ]
      : [];
  return {
    interval,
    metric,
    currency: native ? nativeSymbol[token.chain] : "USD",
    items,
    pool_switches: switches.filter((s) => s.t >= start && s.t <= end + step),
  };
}

function activityAt(
  token: MockToken,
  k: number,
  anchor: number
): Activity | (LiquidityEvent & { kind: "add" | "remove" }) {
  const gap = Math.max(1, Math.round(3600 / Math.max(1, token.tradesPerHour)));
  const t = anchor - k * gap * 1000 - Math.floor(unit(`${token.address}:gap:${k}`) * gap * 500);
  const key = `${anchor}:${k}`;
  const r = rng(hash(`${token.address}:act:${key}`));
  const price = priceAt(token, t);
  const liquidity = k % 17 === 9;
  const buy = r() < 0.56;
  const value = (20 + r() * r() * 4000) * (token.baseLiquidity ? 1 : 0.2);
  const pools = poolsOf(token, t);
  const pool = pools[k % Math.max(1, pools.filter((p) => p.status === "active").length)];
  const wallet = walletFor(token, `${Math.floor(t / 1000)}:${k}`, 0.06);
  const quoteAmount = value / nativeUsd[token.chain];
  const tokenAmount = price ? value / price : value * 1000;
  const base = {
    id: `${Math.floor(t / 400)}:${k % 200}:${liquidity ? 1 : 0}`,
    time: new Date(t).toISOString(),
    token_amount: dec(tokenAmount, 10),
    quote_amount: dec(quoteAmount, 8),
    quote_symbol: pool.quote_symbol,
    value_usd: price === null ? null : dec(value, 8),
    tx_hash: txHash(token.chain, `${token.address}:${key}`),
    pool: pool.address,
    dex: pool.dex,
  };
  if (liquidity) return { ...base, kind: buy ? "add" : "remove", wallet };
  const unattributed = r() < 0.04;
  return {
    ...base,
    kind: buy ? "buy" : "sell",
    wallet: unattributed ? null : wallet,
    attribution: unattributed ? null : r() < 0.9 ? "confirmed" : "inferred",
    price_usd: price === null ? null : dec(price, 8),
    market_cap_usd: price === null ? null : dec(price * token.supply, 8),
  };
}

function activities(token: MockToken, params: URLSearchParams, now: number) {
  const kind = params.get("kind") ?? "all";
  const label = params.get("label") ?? "all";
  if (!["all", "trade", "liquidity"].includes(kind))
    throw new HttpError(400, "invalid_param", "kind", "kind");
  const limit = int(params, "limit", 50, 100);
  const cursor = cursorDecode(params.get("cursor"));
  const anchor = cursor?.anchor ?? Math.floor(now / 5000) * 5000;
  const from = params.get("from") ? Number(params.get("from")) * 1000 : null;
  const to = params.get("to") ? Number(params.get("to")) * 1000 : null;
  const items: Activity[] = [];
  let k = cursor?.offset ?? 0;
  const maxScan = k + limit * 60;
  for (; k < maxScan && items.length < limit; k++) {
    const a = activityAt(token, k, anchor);
    const t = Date.parse(a.time);
    if (t < token.launchAt) break;
    if (from !== null && t < from) break;
    if (to !== null && t >= to) continue;
    const isTrade = a.kind === "buy" || a.kind === "sell";
    if (kind === "trade" && !isTrade) continue;
    if (kind === "liquidity" && isTrade) continue;
    if (label !== "all" && !a.wallet?.labels.includes(label as WalletRef["labels"][number]))
      continue;
    if (isTrade) items.push(a as Activity);
    else
      items.push({
        ...(a as LiquidityEvent),
        attribution: "confirmed",
        price_usd: null,
        market_cap_usd: null,
      } as Activity);
  }
  const exhausted = k >= maxScan || items.length < limit;
  const total24h = metrics(token, "24h", now).trades;
  return {
    items,
    next_cursor: exhausted && items.length < limit ? null : cursorEncode(anchor, k),
    label_counts: { all: total24h, smart_money: Math.round(total24h * token.smShare) },
  };
}

function liquidityEvents(token: MockToken, params: URLSearchParams, now: number) {
  params.set("kind", "liquidity");
  const res = activities(token, params, now);
  return {
    items: res.items.map(
      ({
        id,
        time,
        kind,
        token_amount,
        quote_amount,
        quote_symbol,
        value_usd,
        wallet,
        tx_hash,
        pool,
        dex,
      }) => ({
        id,
        time,
        kind: kind as "add" | "remove",
        token_amount,
        quote_amount,
        quote_symbol,
        value_usd,
        wallet,
        tx_hash,
        pool,
        dex,
      })
    ),
    next_cursor: res.next_cursor,
  };
}

function holders(token: MockToken, params: URLSearchParams, now: number) {
  const limit = int(params, "limit", 50, 100);
  const label = params.get("label") ?? "all";
  const offset = cursorDecode(params.get("cursor"))?.offset ?? 0;
  const cov = coverage(token, now);
  const price = priceAt(token, now);
  const total =
    label === "smart_money"
      ? Math.min(token.smHolders, smWallets(token.chain).length)
      : token.holders;
  const weights = Array.from(
    { length: Math.min(total, 500) },
    (_, i) => 1 / Math.pow(i + 1.6, 1.15)
  );
  const sum = weights.reduce((a, b) => a + b, 0) / 0.82;
  const items: Holder[] = [];
  for (let i = offset; i < Math.min(offset + limit, weights.length); i++) {
    const share = (weights[i] / sum) * 100;
    const wallet =
      label === "smart_money" ? smWalletAt(token, i)! : walletFor(token, `holder-${i}`, 0);
    const r = rng(hash(`${token.address}:h:${i}`));
    const known = cov.status === "full";
    items.push({
      rank: i + 1,
      wallet,
      amount: dec((share / 100) * token.supply, 10),
      share_pct: dec(share, 6),
      native_balance: r() < 0.15 ? null : dec(r() * r() * 400, 6),
      avg_buy_price_usd: known && price ? dec(price * (0.4 + r() * 0.8), 8) : null,
      avg_sell_price_usd: known && price && r() < 0.5 ? dec(price * (0.6 + r()), 8) : null,
      realized_pnl_usd: null,
      unrealized_pnl_usd: null,
    });
  }
  const next = offset + limit;
  return {
    coverage: cov,
    total: cov.status === "full" ? total : null,
    items,
    next_cursor: next < weights.length ? cursorEncode(0, next) : null,
  };
}

function tokenSmartMoney(token: MockToken, now: number) {
  const m = metrics(token, "24h", now);
  const count = Math.min(token.smHolders, 12, smWallets(token.chain).length);
  return {
    list_version: String(listVersionRef.current),
    holders: token.smHolders,
    items: Array.from({ length: count }, (_, i) => {
      const r = rng(hash(`${token.address}:smh:${i}`));
      const share = (2.4 / (i + 1)) * (0.6 + r());
      return {
        wallet: smWalletAt(token, i)!,
        balance: dec((share / 100) * token.supply, 10),
        share_pct: dec(share, 6),
        buy_volume_usd:
          m.smBuyVolume === null ? null : dec((m.smBuyVolume / count) * (0.5 + r()), 8),
        sell_volume_usd: m.smSellVolume === null ? null : dec((m.smSellVolume / count) * r(), 8),
        last_trade_at: new Date(now - Math.floor(r() * 6 * 3_600_000)).toISOString(),
      };
    }),
  };
}

// ---------------------------------------------------------------- market & overview

function natives(now: number) {
  return {
    items: chainList.map((chain) => {
      const drift = 1 + 0.004 * Math.sin(now / 600_000 + chain.length);
      return {
        chain,
        symbol: nativeSymbol[chain],
        price_usd: dec(nativeUsd[chain] * drift, 8),
        change_24h_pct: dec([2.84, -1.12, 0.64][chainList.indexOf(chain)] + drift - 1, 4),
        stale: false,
        as_of: new Date(Math.floor(now / 60_000) * 60_000).toISOString(),
      };
    }),
  };
}

function overview(params: URLSearchParams, now: number) {
  const window = windowParam(params);
  const chain = params.get("chain");
  const list = tokens.filter((t) => !chain || t.chain === chain);
  let net = 0;
  let prev = 0;
  let buy = 0;
  let sell = 0;
  let traded = 0;
  let fresh = 0;
  const seconds = windowSeconds[window];
  const bars = Array.from({ length: 16 }, () => 0);
  for (const t of list) {
    const m = metrics(t, window, now);
    if (!m.trades) continue;
    traded += 1;
    if (now - t.launchAt < seconds * 1000) fresh += 1;
    net += m.netInflow ?? 0;
    buy += m.smBuyVolume ?? 0;
    sell += m.smSellVolume ?? 0;
    const p = metrics(t, window, now - seconds * 1000);
    prev += p.netInflow ?? 0;
    const share = (m.netInflow ?? 0) / 16;
    for (let i = 0; i < 16; i++)
      bars[i] += share * (0.4 + 1.2 * unit(`${t.address}:bar:${i}:${window}`));
  }
  const members = adminState.members.filter((m) => !m.excluded && (!chain || m.chain === chain));
  return {
    as_of: new Date(now).toISOString(),
    list_version: String(listVersionRef.current),
    window,
    net_inflow_usd: dec(net, 10),
    net_inflow_change_pct: prev === 0 ? null : dec(((net - prev) / Math.abs(prev)) * 100, 6),
    active_wallets: Math.round(members.length * 9 * Math.min(1, 0.25 + seconds / 14400)),
    tracked_tokens: traded,
    new_tokens: fresh,
    buy_volume_usd: dec(buy, 10),
    sell_volume_usd: dec(sell, 10),
    buy_share_pct: buy + sell === 0 ? null : dec((buy / (buy + sell)) * 100, 6),
    inflow_bars: bars.map((b) => dec(b, 8)),
  };
}

function facets(params: URLSearchParams, now: number) {
  const window = windowParam(params);
  const chain = params.get("chain");
  const agg = (
    ids: (t: MockToken) => string[],
    list: { id: string; name: string; chain: Chain }[]
  ) =>
    list
      .filter((x) => !chain || x.chain === chain)
      .map((x) => {
        let volume = 0;
        let count = 0;
        for (const t of tokens) {
          if (!ids(t).includes(x.id)) continue;
          const m = metrics(t, window, now);
          if (!m.trades) continue;
          count += 1;
          volume += m.volume ?? 0;
        }
        return {
          id: x.id,
          name: x.name,
          chain: x.chain,
          volume_usd: dec(volume, 10),
          token_count: count,
        };
      })
      .sort((a, b) => Number(b.volume_usd) - Number(a.volume_usd));
  return {
    dexes: agg((t) => t.dexes, dexes),
    platforms: agg((t) => (t.platform ? [t.platform] : []), platforms),
  };
}

function search(params: URLSearchParams, now: number) {
  const q = (params.get("q") ?? "").trim();
  if (!q) throw new HttpError(400, "invalid_param", "q required", "q");
  const chain = params.get("chain");
  const limit = int(params, "limit", 10, 20);
  const lower = q.toLowerCase();
  const matches = tokens
    .filter((t) => !chain || t.chain === chain)
    .map((t) => {
      const addr = t.address.toLowerCase();
      const score =
        addr === lower
          ? 0
          : t.symbol.toLowerCase() === lower
            ? 1
            : t.symbol.toLowerCase().startsWith(lower)
              ? 2
              : t.name.toLowerCase().includes(lower)
                ? 3
                : addr.startsWith(lower) && lower.length >= 4
                  ? 4
                  : -1;
      return { t, score };
    })
    .filter((x) => x.score >= 0)
    .sort((a, b) => a.score - b.score || b.t.holders - a.t.holders)
    .slice(0, limit)
    .map((x) => searchToken(x.t, now));
  const wallets = adminState.members
    .filter(
      (m) =>
        (!chain || m.chain === chain) &&
        (m.address.toLowerCase() === lower || (m.label ?? "").toLowerCase().includes(lower))
    )
    .slice(0, 5)
    .map((m) => ({
      chain: m.chain,
      address: m.address,
      label: m.label,
      is_smart_money: !m.excluded,
    }));
  return { tokens: matches, wallets };
}

// ---------------------------------------------------------------- admin

function adminToken() {
  return process.env.RADAR_MOCK_ADMIN_TOKEN || "mock-admin";
}

function isAdmin(request: Request) {
  const header = request.headers.get("authorization") ?? "";
  const given = Buffer.from(header.startsWith("Bearer ") ? header.slice(7) : "");
  const expected = Buffer.from(adminToken());
  return given.length === expected.length && timingSafeEqual(given, expected);
}

async function createImport(request: Request, now: number) {
  const form = await request.formData().catch(() => null);
  const file = form?.get("file");
  if (!file || typeof file === "string")
    throw new HttpError(400, "invalid_file", "file required", "file");
  if (file.size > MAX_BYTES) throw new HttpError(413, "file_too_large", "file exceeds 5MB", "file");
  const text = await file.text();
  try {
    const rows = validateRecords(readRecords(file.name, text), isMember);
    const totals = { rows: rows.length, valid: 0, invalid: 0, duplicate: 0, existing: 0 };
    for (const r of rows) totals[r.status] += 1;
    const id = `imp_${hash(`${file.name}:${now}:${Math.random()}`).toString(16)}`;
    const report = {
      id,
      file_name: file.name,
      created_at: new Date(now).toISOString(),
      expires_at: new Date(now + 30 * 60_000).toISOString(),
      applied: false,
      totals,
      rows: [
        ...rows.filter((r) => r.status === "invalid"),
        ...rows.filter((r) => r.status !== "invalid"),
      ].slice(0, Math.max(500, totals.invalid)),
    };
    adminState.imports.set(id, { report, all: rows });
    return json(report, 201);
  } catch (e) {
    if (e instanceof ImportFileError) throw new HttpError(e.status, e.code, e.message, "file");
    throw e;
  }
}

function applyImport(id: string, now: number) {
  const stored = adminState.imports.get(id);
  if (!stored) throw new HttpError(404, "not_found", "import not found");
  const { report, all } = stored;
  if (report.applied || Date.parse(report.expires_at) < now) {
    throw new HttpError(409, "import_closed", "already applied or expired");
  }
  let added = 0;
  let merged = 0;
  for (const row of all) {
    if ((row.status !== "valid" && row.status !== "existing") || !row.chain || !row.address)
      continue;
    const id2 = memberId(row.chain, row.address);
    const existing = adminState.members.find((m) => memberId(m.chain, m.address) === id2);
    if (existing) {
      existing.sources = Array.from(new Set([...existing.sources, "import" as const]));
      existing.tags = Array.from(new Set([...existing.tags, ...row.tags]));
      existing.label = existing.label ?? row.label;
      existing.twitter = existing.twitter ?? row.twitter;
      merged += 1;
    } else {
      adminState.members.unshift({
        chain: row.chain as Chain,
        address: row.address,
        label: row.label,
        tags: row.tags,
        twitter: row.twitter,
        sources: ["import"],
        added_at: new Date(now).toISOString(),
        excluded: false,
      });
      added += 1;
    }
  }
  report.applied = true;
  return bumpVersion({ added, merged, removed: 0, total_members: adminState.members.length });
}

function listMembers(params: URLSearchParams) {
  const chain = params.get("chain");
  const source = params.get("source") ?? "all";
  const tag = params.get("tag");
  const q = params.get("q")?.toLowerCase();
  const limit = int(params, "limit", 50, 100);
  const offset = cursorDecode(params.get("cursor"))?.offset ?? 0;
  const list = adminState.members.filter(
    (m) =>
      (!chain || m.chain === chain) &&
      (source === "all" || m.sources.includes(source as "import" | "auto")) &&
      (!tag || m.tags.includes(tag)) &&
      (!q || m.address.toLowerCase().startsWith(q) || (m.label ?? "").toLowerCase().includes(q))
  );
  return {
    list_version: String(listVersionRef.current),
    total: list.length,
    items: list.slice(offset, offset + limit),
    next_cursor: offset + limit < list.length ? cursorEncode(0, offset + limit) : null,
  };
}

async function admin(
  request: Request,
  method: string,
  rest: string[],
  params: URLSearchParams,
  now: number
) {
  if (!isAdmin(request)) return error(403, "forbidden", "admin token required");
  const path = rest.join("/");
  if (method === "GET" && path === "health") return json(health(now));
  if (method === "POST" && path === "smart-money/imports") return createImport(request, now);
  if (
    method === "POST" &&
    rest[0] === "smart-money" &&
    rest[1] === "imports" &&
    rest[3] === "apply"
  ) {
    return json(applyImport(rest[2], now));
  }
  if (method === "GET" && path === "smart-money/members") return json(listMembers(params));
  if (
    method === "DELETE" &&
    rest[0] === "smart-money" &&
    rest[1] === "members" &&
    rest.length === 4
  ) {
    const id = memberId(rest[2], rest[3]);
    const m = adminState.members.find((x) => memberId(x.chain, x.address) === id);
    if (!m || !m.sources.includes("import"))
      throw new HttpError(404, "not_found", "imported member not found");
    m.sources = m.sources.filter((s) => s !== "import");
    if (!m.sources.length) adminState.members = adminState.members.filter((x) => x !== m);
    return json(
      bumpVersion({ added: 0, merged: 0, removed: 1, total_members: adminState.members.length })
    );
  }
  if (path === "smart-money/exclusions" && method === "GET") {
    const chain = params.get("chain");
    return json({
      items: adminState.exclusions.filter((e) => !chain || e.chain === chain),
      next_cursor: null,
    });
  }
  if (path === "smart-money/exclusions" && method === "POST") {
    const body = (await request.json().catch(() => null)) as {
      chain?: string;
      address?: string;
      reason?: string;
    } | null;
    if (!body?.chain || !chainList.includes(body.chain as Chain))
      throw new HttpError(400, "invalid_param", "chain", "chain");
    if (!body.address) throw new HttpError(400, "invalid_param", "address", "address");
    if (!body.reason?.trim()) throw new HttpError(400, "invalid_param", "reason", "reason");
    const id = memberId(body.chain, body.address);
    if (!adminState.exclusions.some((e) => memberId(e.chain, e.address) === id)) {
      adminState.exclusions.unshift({
        chain: body.chain as Chain,
        address: body.address,
        reason: body.reason.trim().slice(0, 200),
        created_at: new Date(now).toISOString(),
      });
    }
    const m = adminState.members.find((x) => memberId(x.chain, x.address) === id);
    if (m) m.excluded = true;
    return json(
      bumpVersion({
        added: 0,
        merged: 0,
        removed: m ? 1 : 0,
        total_members: adminState.members.length,
      })
    );
  }
  if (
    method === "DELETE" &&
    rest[0] === "smart-money" &&
    rest[1] === "exclusions" &&
    rest.length === 4
  ) {
    const id = memberId(rest[2], rest[3]);
    const before = adminState.exclusions.length;
    adminState.exclusions = adminState.exclusions.filter(
      (e) => memberId(e.chain, e.address) !== id
    );
    if (before === adminState.exclusions.length)
      throw new HttpError(404, "not_found", "not excluded");
    const m = adminState.members.find((x) => memberId(x.chain, x.address) === id);
    if (m) m.excluded = false;
    return json(
      bumpVersion({
        added: m ? 1 : 0,
        merged: 0,
        removed: 0,
        total_members: adminState.members.length,
      })
    );
  }
  if (method === "POST" && rest[0] === "parse-failures" && rest[2] === "retry") {
    const f = adminState.failures.find((x) => x.id === rest[1]);
    if (!f) throw new HttpError(404, "not_found", "failure not found");
    f.status = "queued";
    f.last_attempt_at = new Date(now).toISOString();
    return json(f, 202);
  }
  return error(404, "not_found", "no such admin endpoint");
}

// ---------------------------------------------------------------- dispatch

/** Same trace rules as the Go API (18-observability §3): reuse a valid X-Trace-Id/traceparent. */
function traceOf(request: Request): string {
  const header = (request.headers.get("x-trace-id") ?? "").toLowerCase();
  if (/^[0-9a-f]{32}$/.test(header) && !/^0+$/.test(header)) return header;
  const tp = request.headers.get("traceparent")?.split("-")[1]?.toLowerCase() ?? "";
  if (/^[0-9a-f]{32}$/.test(tp) && !/^0+$/.test(tp)) return tp;
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) =>
    b.toString(16).padStart(2, "0")
  ).join("");
}

export async function handleMock(request: Request, segments: string[]): Promise<Response> {
  const traceId = traceOf(request);
  const res = await dispatch(request, segments);
  const headers = new Headers(res.headers);
  headers.set("x-trace-id", traceId);
  if (res.status < 400) return new Response(res.body, { status: res.status, headers });
  const body = (await res.json()) as { error: Record<string, unknown> };
  body.error.trace_id = traceId;
  return new Response(JSON.stringify(body), { status: res.status, headers });
}

async function dispatch(request: Request, segments: string[]): Promise<Response> {
  const now = Date.now();
  const url = new URL(request.url);
  const params = url.searchParams;
  const method = request.method.toUpperCase();
  try {
    const [head, ...rest] = segments;
    if (head === "admin") return await admin(request, method, rest, params, now);
    if (method === "GET" && head === "market" && rest[0] === "natives") return json(natives(now));
    if (method === "GET" && head === "search") return json(search(params, now));
    if (method === "GET" && head === "smart-money" && rest[0] === "overview")
      return json(overview(params, now));
    if (
      method === "GET" &&
      head === "smart-money" &&
      rest[0] === "inflow" &&
      rest[1] === "facets"
    ) {
      return json(facets(params, now));
    }
    if (method === "GET" && head === "smart-money" && rest[0] === "inflow" && rest.length === 1) {
      const limit = int(params, "limit", 50, 100);
      const offset = int(params, "offset", 0, 2000);
      const queryId = params.get("query_id") ?? newSnapshot(params, now);
      return json(page(queryId, params.get("query_id") ? offset : 0, limit, now));
    }
    if (head === "tokens" && rest[0] === "batch" && method === "POST") {
      const body = (await request.json().catch(() => null)) as {
        window?: string;
        currency?: string;
        tokens?: { chain: string; address: string }[];
      } | null;
      if (!body?.tokens?.length || body.tokens.length > 100) {
        throw new HttpError(400, "invalid_param", "1-100 tokens required", "tokens");
      }
      const window = (body.window ?? "1h") as Window;
      if (!(window in windowSeconds)) throw new HttpError(400, "invalid_param", "window", "window");
      const items = body.tokens.flatMap((k) => {
        const t = findToken(k.chain, k.address);
        return t ? [inflowRow(t, window, body.currency === "native", now)] : [];
      });
      return json({ as_of: new Date(now).toISOString(), items });
    }
    if (method === "GET" && head === "tokens" && rest.length >= 2) {
      const token = requireToken(rest[0], rest[1]);
      const native = params.get("currency") === "native";
      switch (rest[2]) {
        case undefined:
          return json(tokenDetail(token, native, now));
        case "candles":
          return json(candles(token, params, now));
        case "activities":
          return json(activities(token, params, now));
        case "holders":
          return json(holders(token, params, now));
        case "pools":
          return json({ items: poolsOf(token, now) });
        case "liquidity-events":
          return json(liquidityEvents(token, params, now));
        case "smart-money":
          return json(tokenSmartMoney(token, now));
      }
    }
    return error(404, "not_found", "no such endpoint");
  } catch (e) {
    if (e instanceof HttpError) return error(e.status, e.code, e.message, e.field);
    console.error("mock api error", e);
    return error(500, "internal", "mock error");
  }
}
