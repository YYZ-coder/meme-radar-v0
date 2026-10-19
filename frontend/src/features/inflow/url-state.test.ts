import { describe, expect, it } from "vitest";
import {
  countFilters,
  inflowApiQuery,
  inflowParamEntries,
  invalidRanges,
  parseInflowState,
} from "./url-state";

describe("inflow URL state", () => {
  it("falls back to defaults for missing or invalid params", () => {
    const s = parseInflowState(
      new URLSearchParams("chain=eth&window=2h&sort=foo&order=x&min_liquidity=abc")
    );
    expect(s.chain).toBeNull();
    expect(s.window).toBe("1h");
    expect(s.sort).toBe("sm_trades");
    expect(s.order).toBe("desc");
    expect(s.filters.ranges.liquidity.min).toBe("");
  });

  it("round-trips chain, window, facets and ranges", () => {
    const params = new URLSearchParams(
      "chain=solana&window=5m&sort=liquidity&order=asc&dex=pumpswap,raydium&platform=pump_fun&min_liquidity=10000&min_net_inflow=-500&max_age=3600&socials=twitter,telegram&dev_status=sold"
    );
    const s = parseInflowState(params);
    expect(s).toMatchObject({ chain: "solana", window: "5m", sort: "liquidity", order: "asc" });
    expect(s.dex).toEqual(["pumpswap", "raydium"]);
    expect(s.filters.ranges.net_inflow.min).toBe("-500");
    const entries = inflowParamEntries(s);
    expect(entries).toMatchObject({ chain: "solana", window: "5m", dex: "pumpswap,raydium" });
    expect(parseInflowState(new URLSearchParams(entries))).toEqual(s);
    expect(countFilters(s.filters)).toBe(6);
  });

  it("rejects negatives on unsigned fields and decimals on integer fields", () => {
    const s = parseInflowState(new URLSearchParams("min_liquidity=-1&min_trades=1.5&min_age=60"));
    expect(s.filters.ranges.liquidity.min).toBe("");
    expect(s.filters.ranges.trades.min).toBe("");
    expect(s.filters.ranges.age.min).toBe("60");
  });

  it("flags min > max and builds the API query", () => {
    const s = parseInflowState(new URLSearchParams("min_volume=10&max_volume=5&chain=bsc"));
    expect(invalidRanges(s.filters)).toEqual(["volume"]);
    expect(inflowApiQuery(s, "usd")).toEqual({
      window: "1h",
      sort: "sm_trades",
      order: "desc",
      currency: "usd",
      chain: "bsc",
      min_volume: "10",
      max_volume: "5",
    });
  });
});
