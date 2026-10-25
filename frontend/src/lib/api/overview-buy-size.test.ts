import { expect, it } from "vitest";
import { overviewSchema } from "./schemas";

const complete = {
  as_of: "2026-10-22T12:00:00Z",
  list_version: "1",
  window: "1m",
  coverage: "complete",
  net_inflow_usd: "10000",
  net_inflow_change_pct: null,
  active_wallets: 2,
  tracked_tokens: 1,
  new_tokens: 0,
  buy_volume_usd: "10000",
  sell_volume_usd: "0",
  buy_share_pct: "100",
  large_buy_volume_usd: "6000",
  small_buy_volume_usd: "4000",
  large_buy_share_pct: "60",
  small_buy_share_pct: "40",
  inflow_bars: [],
};

it("preserves the public large and small smart-money buy amounts and shares", () => {
  expect(overviewSchema.parse(complete)).toMatchObject({
    large_buy_volume_usd: "6000",
    small_buy_volume_usd: "4000",
    large_buy_share_pct: "60",
    small_buy_share_pct: "40",
  });
});

it("preserves unknown buy-size values as null", () => {
  const unknown = {
    ...complete,
    coverage: "partial",
    large_buy_volume_usd: null,
    small_buy_volume_usd: null,
    large_buy_share_pct: null,
    small_buy_share_pct: null,
  };
  expect(overviewSchema.parse(unknown)).toMatchObject(unknown);
});

it.each([
  { ...complete, large_buy_volume_usd: undefined },
  { ...complete, small_buy_volume_usd: 4000 },
  { ...complete, large_buy_share_pct: "unknown" },
  { ...complete, small_buy_share_pct: undefined },
])("rejects missing or non-decimal buy-size fields", (invalid) => {
  expect(overviewSchema.safeParse(invalid).success).toBe(false);
});
