// Inflow snapshot engine: filter + sort once, cache the ID list under query_id (TTL 10 min),
// then page through current values without reordering (TECHNICAL_PLAN §3.4, Demo 08).
import type { InflowPage, InflowRow, InflowSortKey, Window } from "@/lib/api/types";
import { inflowRow, tokens, findToken, type MockToken } from "./data";
import { hash } from "./random";

const TTL = 10 * 60_000;
const MAX_IDS = 2000;

type Snapshot = {
  ids: string[];
  listVersion: string;
  window: Window;
  native: boolean;
  createdAt: number;
  truncated: boolean;
};

const cache = new Map<string, Snapshot>();

export class HttpError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly field: string | null = null
  ) {
    super(message);
  }
}

export const listVersionRef = { current: 42 };

const rangeFields: Record<string, (r: InflowRow) => number | null> = {
  market_cap: (r) => num(r.market_cap),
  price_change: (r) => num(r.price_change_pct),
  liquidity: (r) => num(r.liquidity),
  trades: (r) => r.trades,
  volume: (r) => num(r.volume),
  net_inflow: (r) => num(r.net_inflow),
  sm_holders: (r) => r.sm_holders,
  sm_trades: (r) => r.sm_trades,
  holders: (r) => r.holders,
  age: (r) => r.age_seconds,
};

const sortValue: Record<InflowSortKey, (r: InflowRow) => number | null> = {
  age: (r) => r.age_seconds,
  sm_holders: (r) => r.sm_holders,
  sm_trades: (r) => r.sm_trades,
  market_cap: (r) => num(r.market_cap),
  liquidity: (r) => num(r.liquidity),
  trades: (r) => r.trades,
  volume: (r) => num(r.volume),
  net_inflow: (r) => num(r.net_inflow),
};

function num(value: string | null): number | null {
  return value === null ? null : Number(value);
}

const windows = ["1m", "5m", "1h", "4h", "24h"];
const sorts = Object.keys(sortValue);

function key(t: MockToken) {
  return `${t.chain}:${t.address}`;
}

function parseNumber(params: URLSearchParams, name: string): number | null {
  const raw = params.get(name);
  if (raw === null || raw === "") return null;
  if (!/^-?\d+(\.\d+)?$/.test(raw)) throw new HttpError(400, "invalid_param", "not a number", name);
  return Number(raw);
}

export function newSnapshot(params: URLSearchParams, now: number): string {
  const window = (params.get("window") ?? "1h") as Window;
  if (!windows.includes(window)) throw new HttpError(400, "invalid_param", "bad window", "window");
  const sort = (params.get("sort") ?? "sm_trades") as InflowSortKey;
  if (!sorts.includes(sort)) throw new HttpError(400, "invalid_param", "bad sort", "sort");
  const desc = params.get("order") !== "asc";
  const chain = params.get("chain");
  const native = params.get("currency") === "native";
  const dex = params.get("dex")?.split(",").filter(Boolean) ?? [];
  const platform = params.get("platform")?.split(",").filter(Boolean) ?? [];
  const socials = params.get("socials")?.split(",").filter(Boolean) ?? [];
  const devStatus = params.get("dev_status");
  const ranges = Object.keys(rangeFields).map((field) => {
    const min = parseNumber(params, `min_${field}`);
    const max = parseNumber(params, `max_${field}`);
    if (min !== null && max !== null && min > max) {
      throw new HttpError(400, "invalid_range", "min > max", `min_${field}`);
    }
    return { field, min, max };
  });

  const rows: { id: string; row: InflowRow }[] = [];
  for (const token of tokens) {
    if (chain && token.chain !== chain) continue;
    if (platform.length && !platform.includes(token.platform ?? "")) continue;
    if (dex.length && !token.dexes.some((d) => dex.includes(d))) continue;
    if (socials.some((s) => !token.socials[s as keyof MockToken["socials"]])) continue;
    if (devStatus) {
      if (token.devHoldingPct === null) continue;
      if (devStatus === "sold" ? token.devHoldingPct > 0 : token.devHoldingPct === 0) continue;
    }
    const row = inflowRow(token, window, native, now);
    if (!row.trades) continue; // list only tokens traded in the window
    let ok = true;
    for (const { field, min, max } of ranges) {
      if (min === null && max === null) continue;
      const v = rangeFields[field](row);
      if (v === null || (min !== null && v < min) || (max !== null && v > max)) {
        ok = false;
        break;
      }
    }
    if (ok) rows.push({ id: key(token), row });
  }
  const get = sortValue[sort];
  rows.sort((a, b) => {
    const va = get(a.row);
    const vb = get(b.row);
    if (va === null && vb === null) return a.id < b.id ? -1 : 1;
    if (va === null) return 1;
    if (vb === null) return -1;
    if (va !== vb) return desc ? vb - va : va - vb;
    return a.id < b.id ? -1 : 1;
  });

  for (const [id, snap] of cache) if (now - snap.createdAt > TTL) cache.delete(id);
  const queryId = `q_${hash(`${params.toString()}:${now}:${Math.random()}`).toString(16)}`;
  cache.set(queryId, {
    ids: rows.slice(0, MAX_IDS).map((r) => r.id),
    listVersion: String(listVersionRef.current),
    window,
    native,
    createdAt: now,
    truncated: rows.length > MAX_IDS,
  });
  return queryId;
}

export function page(queryId: string, offset: number, limit: number, now: number): InflowPage {
  const snap = cache.get(queryId);
  if (!snap || now - snap.createdAt > TTL) {
    cache.delete(queryId);
    throw new HttpError(410, "query_expired", "query_id expired; re-query");
  }
  const ids = snap.ids.slice(offset, offset + limit);
  const items = ids.flatMap((id) => {
    const [chain, address] = id.split(":");
    const token = findToken(chain, address);
    return token ? [inflowRow(token, snap.window, snap.native, now)] : [];
  });
  const next = offset + limit;
  return {
    query_id: queryId,
    list_version: snap.listVersion,
    as_of: new Date(now).toISOString(),
    window: snap.window,
    total: snap.ids.length,
    truncated: snap.truncated,
    offset,
    next_offset: next < snap.ids.length ? next : null,
    items,
  };
}
