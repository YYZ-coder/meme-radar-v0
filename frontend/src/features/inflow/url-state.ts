// The URL is the single source of truth for inflow filters (14-frontend §3).
import { isChain } from "@/lib/chains";
import {
  inflowSortKeys,
  windows,
  type Chain,
  type InflowSortKey,
  type Window,
} from "@/lib/api/types";

export const rangeFields = [
  "market_cap",
  "price_change",
  "liquidity",
  "trades",
  "volume",
  "net_inflow",
  "sm_holders",
  "sm_trades",
  "holders",
  "age",
] as const;
export type RangeField = (typeof rangeFields)[number];

/** Fields that accept negative values. */
const signedFields: ReadonlySet<RangeField> = new Set(["price_change", "net_inflow"]);
/** Integer-only fields. */
export const integerFields: ReadonlySet<RangeField> = new Set([
  "trades",
  "sm_holders",
  "sm_trades",
  "holders",
  "age",
]);

export const socialKeys = ["twitter", "telegram", "github", "website"] as const;
export type SocialKey = (typeof socialKeys)[number];

export type Range = { min: string; max: string };
export type InflowFilters = {
  ranges: Record<RangeField, Range>;
  devStatus: "" | "sold" | "holding";
  socials: SocialKey[];
};

export type InflowState = {
  chain: Chain | null;
  window: Window;
  sort: InflowSortKey;
  order: "asc" | "desc";
  dex: string[];
  platform: string[];
  filters: InflowFilters;
  watch: boolean;
};

export const defaultSort: InflowSortKey = "sm_trades";

export function emptyFilters(): InflowFilters {
  return {
    ranges: Object.fromEntries(rangeFields.map((f) => [f, { min: "", max: "" }])) as Record<
      RangeField,
      Range
    >,
    devStatus: "",
    socials: [],
  };
}

export function validNumber(field: RangeField, value: string | null): string {
  if (value === null || value.trim() === "") return "";
  const text = value.trim();
  const pattern = integerFields.has(field)
    ? /^\d+$/
    : signedFields.has(field)
      ? /^-?\d+(\.\d+)?$/
      : /^\d+(\.\d+)?$/;
  return pattern.test(text) ? text : "";
}

function list(value: string | null): string[] {
  return value
    ? value
        .split(",")
        .map((v) => v.trim())
        .filter((v) => /^[a-z0-9_.-]{1,40}$/.test(v))
    : [];
}

export function parseInflowState(params: URLSearchParams): InflowState {
  const chain = params.get("chain");
  const window = params.get("window");
  const sort = params.get("sort");
  const filters = emptyFilters();
  for (const field of rangeFields) {
    filters.ranges[field] = {
      min: validNumber(field, params.get(`min_${field}`)),
      max: validNumber(field, params.get(`max_${field}`)),
    };
  }
  const dev = params.get("dev_status");
  filters.devStatus = dev === "sold" || dev === "holding" ? dev : "";
  filters.socials = list(params.get("socials")).filter((s): s is SocialKey =>
    (socialKeys as readonly string[]).includes(s)
  );
  return {
    chain: isChain(chain) ? chain : null,
    window: (windows as readonly string[]).includes(window ?? "") ? (window as Window) : "1h",
    sort: (inflowSortKeys as readonly string[]).includes(sort ?? "")
      ? (sort as InflowSortKey)
      : defaultSort,
    order: params.get("order") === "asc" ? "asc" : "desc",
    dex: list(params.get("dex")),
    platform: list(params.get("platform")),
    filters,
    watch: params.get("watch") === "1",
  };
}

/** Params owned by the inflow page; anything else in the URL is preserved. */
export function inflowParamEntries(state: InflowState): Record<string, string> {
  const out: Record<string, string> = {
    chain: state.chain ?? "",
    window: state.window === "1h" ? "" : state.window,
    sort: state.sort === defaultSort ? "" : state.sort,
    order: state.order === "desc" ? "" : state.order,
    dex: state.dex.join(","),
    platform: state.platform.join(","),
    dev_status: state.filters.devStatus,
    socials: state.filters.socials.join(","),
    watch: state.watch ? "1" : "",
  };
  for (const field of rangeFields) {
    out[`min_${field}`] = state.filters.ranges[field].min;
    out[`max_${field}`] = state.filters.ranges[field].max;
  }
  return out;
}

export function countFilters(filters: InflowFilters): number {
  let n = 0;
  for (const field of rangeFields) {
    if (filters.ranges[field].min || filters.ranges[field].max) n += 1;
  }
  if (filters.devStatus) n += 1;
  return n + filters.socials.length;
}

/** Fields where min > max (shown in red in the drawer, mirrors API 400). */
export function invalidRanges(filters: InflowFilters): RangeField[] {
  return rangeFields.filter((field) => {
    const { min, max } = filters.ranges[field];
    return min !== "" && max !== "" && Number(min) > Number(max);
  });
}

/** Query sent to GET /smart-money/inflow for a new snapshot. */
export function inflowApiQuery(
  state: InflowState,
  currency: "usd" | "native"
): Record<string, string> {
  const q: Record<string, string> = {
    window: state.window,
    sort: state.sort,
    order: state.order,
    currency,
  };
  if (state.chain) q.chain = state.chain;
  if (state.dex.length) q.dex = state.dex.join(",");
  if (state.platform.length) q.platform = state.platform.join(",");
  if (state.filters.devStatus) q.dev_status = state.filters.devStatus;
  if (state.filters.socials.length) q.socials = state.filters.socials.join(",");
  for (const field of rangeFields) {
    const { min, max } = state.filters.ranges[field];
    if (min) q[`min_${field}`] = min;
    if (max) q[`max_${field}`] = max;
  }
  return q;
}
