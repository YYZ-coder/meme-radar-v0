import { describe, expect, it } from "vitest";
import { bollinger, ema, macd, rsi, sma } from "./indicators";

const closes = [
  44.34, 44.09, 44.15, 43.61, 44.33, 44.83, 45.1, 45.42, 45.84, 46.08, 45.89, 46.03, 45.61, 46.28,
  46.28, 46.0, 46.03, 46.41, 46.22, 45.64,
];

describe("indicators", () => {
  it("computes simple and exponential moving averages", () => {
    expect(sma([1, 2, 3, 4, 5], 3)).toEqual([null, null, 2, 3, 4]);
    const e = ema([1, 2, 3, 4, 5], 3);
    expect(e.slice(0, 2)).toEqual([null, null]);
    expect(e[2]).toBe(2);
    expect(e[3]).toBe(3);
    expect(e[4]).toBe(4);
  });

  it("matches the classic Wilder RSI example", () => {
    // StockCharts' Wilder RSI sample: ≈70.5 after 15 closes, then ≈66.3.
    const r = rsi(closes, 14);
    expect(r.slice(0, 14).every((v) => v === null)).toBe(true);
    expect(r[14]).toBeCloseTo(70.46, 0);
    expect(r[15]).toBeCloseTo(66.25, 0);
  });

  it("builds Bollinger bands symmetric around the mean", () => {
    const b = bollinger([2, 4, 4, 4, 5, 5, 7, 9], 8, 2);
    expect(b.mid[7]).toBe(5);
    expect(b.upper[7]).toBe(9);
    expect(b.lower[7]).toBe(1);
  });

  it("derives MACD histogram as line minus signal", () => {
    const values = Array.from({ length: 60 }, (_, i) => 100 + Math.sin(i / 4) * 5 + i * 0.2);
    const m = macd(values);
    expect(m.line[24]).toBeNull();
    expect(m.line[25]).not.toBeNull();
    expect(m.signal[32]).toBeNull();
    const i = 50;
    expect(m.hist[i]).toBeCloseTo((m.line[i] as number) - (m.signal[i] as number), 12);
  });
});
