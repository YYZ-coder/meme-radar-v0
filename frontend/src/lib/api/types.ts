// Convenience aliases over the generated OpenAPI types (schema.gen.ts is generated; do not edit).
import type { components } from "./schema.gen";

type S = components["schemas"];

export type Chain = S["Chain"];
export type Window = S["Window"];
export type SummaryWindow = S["SummaryWindow"];
export type CandleInterval = S["CandleInterval"];
export type Currency = S["Currency"];
export type ErrorResponse = S["ErrorResponse"];
export type ErrorCode = S["ErrorCode"];
export type NativePricesResponse = S["NativePricesResponse"];
export type SearchResponse = S["SearchResponse"];
export type SearchToken = S["SearchToken"];
export type OverviewResponse = S["OverviewResponse"];
export type FacetsResponse = S["FacetsResponse"];
export type Facet = S["Facet"];
export type InflowSortKey = S["InflowSortKey"];
export type InflowRow = S["InflowRow"];
export type InflowPage = S["InflowPage"];
export type BatchRequest = S["BatchRequest"];
export type BatchResponse = S["BatchResponse"];
export type TokenRef = S["TokenRef"];
export type TokenDetail = S["TokenDetail"];
export type WindowSummary = S["WindowSummary"];
export type CandlesResponse = S["CandlesResponse"];
export type Candle = S["Candle"];
export type Activity = S["Activity"];
export type ActivitiesResponse = S["ActivitiesResponse"];
export type ActivityLabelFilter = S["ActivityLabelFilter"];
export type WalletRef = S["WalletRef"];
export type WalletLabel = S["WalletLabel"];
export type HoldersResponse = S["HoldersResponse"];
export type Holder = S["Holder"];
export type HolderCoverage = S["HolderCoverage"];
export type PoolsResponse = S["PoolsResponse"];
export type Pool = S["Pool"];
export type LiquidityEventsResponse = S["LiquidityEventsResponse"];
export type LiquidityEvent = S["LiquidityEvent"];
export type TokenSmartMoneyResponse = S["TokenSmartMoneyResponse"];
export type ImportReport = S["ImportReport"];
export type ImportRow = S["ImportRow"];
export type ListVersionChange = S["ListVersionChange"];
export type MembersResponse = S["MembersResponse"];
export type Member = S["Member"];
export type ExclusionRequest = S["ExclusionRequest"];
export type ExclusionsResponse = S["ExclusionsResponse"];
export type Exclusion = S["Exclusion"];
export type AdminHealth = S["AdminHealth"];
export type ChainHealth = S["ChainHealth"];
export type ParserHealth = S["ParserHealth"];
export type ParseFailure = S["ParseFailure"];
export type Budget = S["Budget"];

export const windows = ["1m", "5m", "1h", "4h", "24h"] as const satisfies readonly Window[];
export const summaryWindows = ["5m", "1h", "4h", "24h"] as const satisfies readonly SummaryWindow[];
export const candleIntervals = [
  "1s",
  "30s",
  "1m",
  "5m",
  "1h",
  "1d",
] as const satisfies readonly CandleInterval[];
export const inflowSortKeys = [
  "age",
  "sm_holders",
  "sm_trades",
  "market_cap",
  "liquidity",
  "trades",
  "volume",
  "net_inflow",
] as const satisfies readonly InflowSortKey[];

/** Every API error code (api/openapi.yaml ErrorCode); the tuple must stay exhaustive. */
export const errorCodes = [
  "invalid_param",
  "invalid_range",
  "unauthorized",
  "forbidden",
  "not_found",
  "import_closed",
  "query_expired",
  "file_too_large",
  "too_many_rows",
  "invalid_file",
  "secret_detected",
  "rate_limited",
  "busy",
  "api_unavailable",
  "internal",
] as const satisfies readonly ErrorCode[];
type MissingErrorCode = Exclude<ErrorCode, (typeof errorCodes)[number]>;
export const errorCodesExhaustive: MissingErrorCode extends never ? true : never = true;
