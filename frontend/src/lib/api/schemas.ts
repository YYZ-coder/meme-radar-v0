// Runtime validation at the API boundary (frontend/AGENTS.md §2). Each schema is checked
// against the generated OpenAPI type with `satisfies`, so contract drift fails typecheck.
import { z } from "zod";
import * as T from "./types";

const decimal = z.string().regex(/^-?\d+(\.\d+)?$/);
const nDecimal = decimal.nullable();
const int = z.number().int();
const nInt = int.nullable();
const ts = z.string().min(1);
const nTs = ts.nullable();

export const chainSchema = z.enum(["solana", "base", "bsc"]);
export const windowSchema = z.enum(["1m", "5m", "1h", "4h", "24h"]);
const summaryWindowSchema = z.enum(["5m", "1h", "4h", "24h"]);
const intervalSchema = z.enum(["1s", "30s", "1m", "5m", "1h", "1d"]);
const currencySchema = z.enum(["USD", "SOL", "ETH", "BNB"]);
const address = z.string().min(32).max(44);

export const errorCodeSchema = z.enum(T.errorCodes);

export const errorSchema = z.object({
  error: z.object({
    code: errorCodeSchema,
    message: z.string(),
    field: z.string().nullable().optional(),
    trace_id: z.string().regex(/^[0-9a-f]{32}$/),
  }),
}) satisfies z.ZodType<T.ErrorResponse>;

export const nativePricesSchema = z.object({
  items: z.array(
    z.object({
      chain: chainSchema,
      symbol: z.enum(["SOL", "ETH", "BNB"]),
      price_usd: nDecimal,
      change_24h_pct: nDecimal,
      stale: z.boolean(),
      as_of: ts,
    })
  ),
}) satisfies z.ZodType<T.NativePricesResponse>;

const socialsSchema = z.object({
  twitter: z.string().nullable().optional(),
  telegram: z.string().nullable().optional(),
  website: z.string().nullable().optional(),
  github: z.string().nullable().optional(),
});

export const tokenRefSchema = z.object({
  chain: chainSchema,
  address,
  symbol: z.string().max(32),
  name: z.string().max(128),
  image_url: z.string().nullable().optional(),
  launch_platform: z.string().nullable().optional(),
  launch_at: nTs.optional(),
}) satisfies z.ZodType<T.TokenRef>;

const searchTokenSchema = tokenRefSchema.extend({
  market_cap_usd: nDecimal,
  age_seconds: nInt,
  last_trade_at: nTs,
}) satisfies z.ZodType<T.SearchToken>;

export const searchSchema = z.object({
  tokens: z.array(searchTokenSchema),
  wallets: z.array(
    z.object({
      chain: chainSchema,
      address,
      label: z.string().nullable(),
      is_smart_money: z.boolean(),
    })
  ),
}) satisfies z.ZodType<T.SearchResponse>;

export const overviewSchema = z.object({
  as_of: ts,
  list_version: z.string(),
  window: windowSchema,
  net_inflow_usd: nDecimal,
  net_inflow_change_pct: nDecimal,
  active_wallets: nInt,
  tracked_tokens: int,
  new_tokens: nInt,
  buy_volume_usd: nDecimal,
  sell_volume_usd: nDecimal,
  buy_share_pct: nDecimal,
  inflow_bars: z.array(nDecimal).max(16),
}) satisfies z.ZodType<T.OverviewResponse>;

const facetSchema = z.object({
  id: z.string(),
  name: z.string(),
  chain: chainSchema.optional(),
  volume_usd: nDecimal,
  token_count: int,
});

export const facetsSchema = z.object({
  dexes: z.array(facetSchema),
  platforms: z.array(facetSchema),
}) satisfies z.ZodType<T.FacetsResponse>;

export const inflowRowSchema = z.object({
  token: tokenRefSchema,
  currency: currencySchema,
  price: nDecimal,
  market_cap: nDecimal,
  price_change_pct: nDecimal,
  liquidity: nDecimal,
  trades: nInt,
  buys: nInt,
  sells: nInt,
  volume: nDecimal,
  net_inflow: nDecimal,
  sm_holders: nInt,
  sm_trades: nInt,
  holders: nInt,
  age_seconds: nInt,
  dexes: z.array(z.string()),
  socials: socialsSchema,
  sparkline: z.array(nDecimal).max(20),
}) satisfies z.ZodType<T.InflowRow>;

export const inflowPageSchema = z.object({
  query_id: z.string(),
  list_version: z.string(),
  as_of: ts,
  window: windowSchema,
  total: int,
  truncated: z.boolean(),
  offset: int,
  next_offset: nInt,
  items: z.array(inflowRowSchema),
}) satisfies z.ZodType<T.InflowPage>;

export const batchSchema = z.object({
  as_of: ts,
  items: z.array(inflowRowSchema),
}) satisfies z.ZodType<T.BatchResponse>;

const windowSummarySchema = z.object({
  window: summaryWindowSchema,
  volume: nDecimal,
  net_inflow: nDecimal,
  traders: nInt,
  buys: nInt,
  sells: nInt,
  buy_volume: nDecimal,
  sell_volume: nDecimal,
  price_change_pct: nDecimal,
}) satisfies z.ZodType<T.WindowSummary>;

const coverageSchema = z.object({
  status: z.enum(["full", "partial", "unknown"]),
  since: nTs,
  as_of: ts,
}) satisfies z.ZodType<T.HolderCoverage>;

export const tokenDetailSchema = z.object({
  token: tokenRefSchema,
  currency: currencySchema,
  as_of: ts,
  decimals: int,
  total_supply: nDecimal,
  socials: socialsSchema,
  creator: z.object({ address, holding_pct: nDecimal }).nullable(),
  main_pool: z.object({ address, dex: z.string(), quote_symbol: z.string() }).nullable(),
  price: nDecimal,
  market_cap: nDecimal,
  liquidity: nDecimal,
  volume_24h: nDecimal,
  price_change_24h_pct: nDecimal,
  holders: nInt,
  sm_holders: nInt,
  ath_market_cap: nDecimal,
  ath_since: nTs,
  holder_coverage: coverageSchema,
  windows: z.array(windowSummarySchema),
  same_name: z.array(searchTokenSchema),
}) satisfies z.ZodType<T.TokenDetail>;

export const candlesSchema = z.object({
  interval: intervalSchema,
  metric: z.enum(["price", "market_cap"]),
  currency: currencySchema,
  items: z.array(z.object({ t: int, o: decimal, h: decimal, l: decimal, c: decimal, v: decimal })),
  pool_switches: z.array(
    z.object({
      t: int,
      from_pool: z.string().nullable(),
      to_pool: z.string(),
      reason: z.enum(["liquidity", "graduation"]),
    })
  ),
}) satisfies z.ZodType<T.CandlesResponse>;

const walletLabelSchema = z.enum([
  "smart_money",
  "kol",
  "insider",
  "whale",
  "sniper",
  "dev",
  "bundler",
  "pro_trader",
]);

const walletRefSchema = z.object({
  address,
  labels: z.array(walletLabelSchema),
  display_name: z.string().nullable(),
}) satisfies z.ZodType<T.WalletRef>;

const activitySchema = z.object({
  id: z.string(),
  time: ts,
  kind: z.enum(["buy", "sell", "add", "remove"]),
  wallet: walletRefSchema.nullable(),
  attribution: z.enum(["confirmed", "inferred", "ambiguous"]).nullable(),
  token_amount: decimal,
  quote_amount: decimal,
  quote_symbol: z.string(),
  value_usd: nDecimal,
  price_usd: nDecimal,
  market_cap_usd: nDecimal,
  tx_hash: z.string(),
  pool: address,
  dex: z.string(),
}) satisfies z.ZodType<T.Activity>;

export const activitiesSchema = z.object({
  items: z.array(activitySchema),
  next_cursor: z.string().nullable(),
  label_counts: z.record(z.string(), int),
}) satisfies z.ZodType<T.ActivitiesResponse>;

export const holdersSchema = z.object({
  coverage: coverageSchema,
  total: nInt,
  items: z.array(
    z.object({
      rank: int,
      wallet: walletRefSchema,
      amount: decimal,
      share_pct: nDecimal,
      native_balance: nDecimal,
      avg_buy_price_usd: nDecimal,
      avg_sell_price_usd: nDecimal,
      realized_pnl_usd: nDecimal,
      unrealized_pnl_usd: nDecimal,
    })
  ),
  next_cursor: z.string().nullable(),
}) satisfies z.ZodType<T.HoldersResponse>;

export const poolsSchema = z.object({
  items: z.array(
    z.object({
      address,
      dex: z.string(),
      protocol: z.string(),
      quote_symbol: z.string(),
      liquidity_usd: nDecimal,
      reserve_token: nDecimal,
      reserve_quote: nDecimal,
      created_at: nTs,
      status: z.enum(["active", "graduated", "closed"]),
      is_main: z.boolean(),
    })
  ),
}) satisfies z.ZodType<T.PoolsResponse>;

export const liquidityEventsSchema = z.object({
  items: z.array(
    z.object({
      id: z.string(),
      time: ts,
      kind: z.enum(["add", "remove"]),
      token_amount: decimal,
      quote_amount: decimal,
      quote_symbol: z.string(),
      value_usd: nDecimal,
      wallet: walletRefSchema.nullable(),
      tx_hash: z.string(),
      pool: address,
      dex: z.string(),
    })
  ),
  next_cursor: z.string().nullable(),
}) satisfies z.ZodType<T.LiquidityEventsResponse>;

export const tokenSmartMoneySchema = z.object({
  list_version: z.string(),
  holders: nInt,
  items: z.array(
    z.object({
      wallet: walletRefSchema,
      balance: nDecimal,
      share_pct: nDecimal,
      buy_volume_usd: nDecimal,
      sell_volume_usd: nDecimal,
      last_trade_at: nTs,
    })
  ),
}) satisfies z.ZodType<T.TokenSmartMoneyResponse>;

export const importReportSchema = z.object({
  id: z.string(),
  file_name: z.string(),
  created_at: ts,
  expires_at: ts,
  applied: z.boolean(),
  totals: z.object({
    rows: int,
    valid: int,
    invalid: int,
    duplicate: int,
    existing: int,
  }),
  rows: z.array(
    z.object({
      line: int,
      chain: z.string().nullable(),
      address: z.string().nullable(),
      label: z.string().nullable(),
      tags: z.array(z.string()),
      twitter: z.string().nullable(),
      source: z.string().nullable(),
      status: z.enum(["valid", "invalid", "duplicate", "existing"]),
      error: z.string().nullable(),
    })
  ),
}) satisfies z.ZodType<T.ImportReport>;

export const listVersionChangeSchema = z.object({
  list_version: z.string(),
  previous_version: z.string(),
  added: int,
  merged: int,
  removed: int,
  total_members: int,
}) satisfies z.ZodType<T.ListVersionChange>;

export const membersSchema = z.object({
  list_version: z.string(),
  total: int,
  items: z.array(
    z.object({
      chain: chainSchema,
      address,
      label: z.string().nullable(),
      tags: z.array(z.string()),
      twitter: z.string().nullable(),
      sources: z.array(z.enum(["import", "auto"])),
      added_at: ts,
      excluded: z.boolean(),
    })
  ),
  next_cursor: z.string().nullable(),
}) satisfies z.ZodType<T.MembersResponse>;

export const exclusionsSchema = z.object({
  items: z.array(z.object({ chain: chainSchema, address, reason: z.string(), created_at: ts })),
  next_cursor: z.string().nullable(),
}) satisfies z.ZodType<T.ExclusionsResponse>;

export const parseFailureSchema = z.object({
  id: z.string(),
  chain: chainSchema,
  module: z.string(),
  position: z.string(),
  error: z.string(),
  attempts: int,
  status: z.enum(["failed", "queued"]),
  last_attempt_at: ts,
}) satisfies z.ZodType<T.ParseFailure>;

export const adminHealthSchema = z.object({
  as_of: ts,
  chains: z.array(
    z.object({
      chain: chainSchema,
      head: nInt,
      anchor: int,
      lower_bound: int,
      forward_position: nInt,
      history_position: nInt,
      backlog_blocks: nInt,
      lag_seconds: nInt,
      gaps_open: int,
      gaps_by_reason: z.record(z.string(), int),
      last_error: z
        .object({
          kind: z.enum(["retryable", "config", "not_found", "history_unavailable", "incomplete"]),
          message: z.string(),
          at: ts,
        })
        .nullable(),
      budget_paused: z.boolean(),
    })
  ),
  parsers: z.array(
    z.object({
      chain: chainSchema,
      module: z.string(),
      version: z.string(),
      position: nInt,
      lag_blocks: nInt,
      quarantined: int,
      failures: int,
      unknown_discriminators: int,
    })
  ),
  failures: z.array(parseFailureSchema),
  budget: z.object({
    month: z.string(),
    spent_usd: decimal,
    limit_usd: decimal,
    state: z.enum(["normal", "warn", "history_paused", "paused"]),
  }),
}) satisfies z.ZodType<T.AdminHealth>;
