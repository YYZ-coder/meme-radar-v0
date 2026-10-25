import { expect, it } from "vitest";
import { candlesSchema } from "./schemas";

const partial = {
  interval: "1m",
  metric: "price",
  currency: "USD",
  as_of: "2026-10-21T12:01:30Z",
  coverage: "partial",
  empty_reason: null,
  items: [],
  observed_items: [{ t: 1792584060, o: "2", h: "4", l: "2", c: "3", v: null, closed: false }],
  gaps: [{ from: 1792584000, to: 1792584060, reason: "price_unavailable" }],
  pool_switches: [],
};

it("preserves observed main-pool prices without inventing complete candles or volume", () => {
  expect(candlesSchema.parse(partial)).toMatchObject(partial);
});

// Regression checks for the frozen non-null complete-candle contract.
it.each(["o", "h", "l", "c", "v"])("rejects NULL %s in complete candles", (field) => {
  const candle = { t: 1792584000, o: "2", h: "4", l: "2", c: "3", v: "6", [field]: null };
  expect(candlesSchema.safeParse({ ...partial, items: [candle] }).success).toBe(false);
});

it.each(["o", "h", "l", "c"])(
  "rejects NULL observed %s instead of drawing a fake price",
  (field) => {
    expect(
      candlesSchema.safeParse({
        ...partial,
        observed_items: [{ ...partial.observed_items[0], [field]: null }],
      }).success
    ).toBe(false);
  }
);

it.each(["as_of", "coverage", "empty_reason", "observed_items", "gaps"])(
  "requires the candle coverage field %s",
  (field) => {
    const response: Record<string, unknown> = { ...partial };
    delete response[field];
    expect(candlesSchema.safeParse(response).success).toBe(false);
  }
);

it.each(["no_main_pool", "no_price", "no_trades", null])(
  "preserves the empty reason %s",
  (empty_reason) => {
    expect(candlesSchema.parse({ ...partial, observed_items: [], empty_reason }).empty_reason).toBe(
      empty_reason
    );
  }
);
