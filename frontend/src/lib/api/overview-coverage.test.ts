import { expect, it } from "vitest";
import { overviewSchema } from "./schemas";

const partial = {
  as_of: "2026-10-21T12:00:00Z",
  list_version: "1",
  window: "1m",
  coverage: "partial",
  net_inflow_usd: null,
  net_inflow_change_pct: null,
  active_wallets: null,
  tracked_tokens: null,
  new_tokens: null,
  buy_volume_usd: null,
  sell_volume_usd: null,
  buy_share_pct: null,
  large_buy_volume_usd: null,
  small_buy_volume_usd: null,
  large_buy_share_pct: null,
  small_buy_share_pct: null,
  inflow_bars: [null, null],
};

it("preserves incomplete overview coverage without replacing unknown tracked totals", () => {
  expect(overviewSchema.parse(partial)).toMatchObject(partial);
});

it("preserves complete count coverage independently of missing USD prices", () => {
  const complete = { ...partial, coverage: "complete", tracked_tokens: 3 };
  expect(overviewSchema.parse(complete)).toMatchObject(complete);
});

it.each([
  { ...partial, coverage: "unknown" },
  { ...partial, tracked_tokens: -1 },
  { ...partial, tracked_tokens: 1.5 },
])("rejects invalid overview coverage or token counts", (invalid) => {
  expect(overviewSchema.safeParse(invalid).success).toBe(false);
});
