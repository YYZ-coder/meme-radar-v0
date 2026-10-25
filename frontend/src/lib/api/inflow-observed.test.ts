import { expect, it } from "vitest";
import { inflowRowSchema } from "./schemas";

const row = {
  token: { chain: "base", address: "0x" + "11".repeat(20), symbol: "X", name: "X" },
  currency: "USD",
  price: null,
  market_cap: null,
  price_change_pct: null,
  liquidity: null,
  trades: null,
  buys: null,
  sells: null,
  volume: null,
  net_inflow: null,
  sm_holders: null,
  sm_trades: null,
  holders: null,
  age_seconds: null,
  dexes: [],
  socials: {},
  sparkline: [],
  observed: { trades: 3, buys: 2, sells: 1 },
  coverage: "partial",
};

it("preserves independent observations through the real API runtime boundary", () => {
  const parsed = inflowRowSchema.parse(row);
  expect(parsed).toMatchObject({
    observed: { trades: 3, buys: 2, sells: 1 },
    coverage: "partial",
    trades: null,
    buys: null,
    sells: null,
    volume: null,
  });
});

it.each([
  { ...row, observed: { trades: -1, buys: 0, sells: 0 } },
  { ...row, observed: { trades: 1.5, buys: 1, sells: 0 } },
  { ...row, coverage: "probably" },
])("rejects untrusted observation values or coverage", (invalid) => {
  expect(inflowRowSchema.safeParse(invalid).success).toBe(false);
});
