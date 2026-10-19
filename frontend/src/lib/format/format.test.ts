import { describe, expect, it } from "vitest";
import { shortAddress } from "./address";
import {
  formatAmount,
  formatCount,
  formatPercent,
  formatPrice,
  formatQuantity,
  toneOf,
} from "./number";
import { formatAge, secondsSince } from "./time";

describe("formatAmount", () => {
  it("uses 万/亿 in Chinese and K/M/B in English", () => {
    expect(formatAmount("28400000", "zh-CN")).toBe("$2840.00万");
    expect(formatAmount("312000000", "zh-CN")).toBe("$3.12亿");
    expect(formatAmount("28400000", "en")).toBe("$28.40M");
    expect(formatAmount("1240", "en")).toBe("$1.24K");
    expect(formatAmount("4120000000", "en")).toBe("$4.12B");
  });
  it("keeps unknown and known zero apart", () => {
    expect(formatAmount(null, "zh-CN")).toBe("--");
    expect(formatAmount(undefined, "en")).toBe("--");
    expect(formatAmount("0", "en")).toBe("$0");
  });
  it("signs net inflow and supports native currency", () => {
    expect(formatAmount("-42860", "zh-CN", "USD", { signed: true })).toBe("-$4.29万");
    expect(formatAmount("2052.62", "en", "USD", { signed: true })).toBe("+$2.05K");
    expect(formatAmount("12.5", "en", "SOL")).toBe("12.5 SOL");
  });
});

describe("formatPrice", () => {
  it("renders subscript zeros for tiny prices", () => {
    expect(formatPrice("0.00000412")).toBe("$0.0₅412");
    expect(formatPrice("0.0000322262")).toBe("$0.0₄3222");
  });
  it("keeps ordinary prices readable", () => {
    expect(formatPrice("0.00728")).toBe("$0.00728");
    expect(formatPrice("105.247")).toBe("$105.25");
    expect(formatPrice("1.23456")).toBe("$1.2346");
    expect(formatPrice("2454.07")).toBe("$2,454.07");
    expect(formatPrice(null)).toBe("--");
    expect(formatPrice("0.0012", "BNB")).toBe("0.0012 BNB");
  });
});

describe("other formatters", () => {
  it("formats percent with sign", () => {
    expect(formatPercent("24.68")).toBe("+24.68%");
    expect(formatPercent("-6.2")).toBe("-6.20%");
    expect(formatPercent("0")).toBe("0.00%");
    expect(formatPercent(null)).toBe("--");
  });
  it("formats counts, quantities and tone", () => {
    expect(formatCount(2846, "en")).toBe("2,846");
    expect(formatCount(null, "en")).toBe("--");
    expect(formatQuantity("1000000000", "en")).toBe("1.00B");
    expect(toneOf("-1")).toBe("negative");
    expect(toneOf("3")).toBe("positive");
    expect(toneOf(null)).toBe("");
  });
  it("formats compact token age", () => {
    expect(formatAge(42)).toBe("42s");
    expect(formatAge(5 * 60)).toBe("5m");
    expect(formatAge(23 * 3600 + 39 * 60)).toBe("23h39m");
    expect(formatAge(12 * 86400)).toBe("12d");
    expect(formatAge(95 * 86400)).toBe("3mo");
    expect(formatAge(400 * 86400)).toBe("1y");
    expect(formatAge(null)).toBe("--");
    expect(secondsSince("2026-10-16T00:00:00Z", Date.parse("2026-10-16T00:01:00Z"))).toBe(60);
  });
  it("shortens addresses 4…4", () => {
    expect(shortAddress("GX1JGwFmrzgkv6MBjwZZdZt3qBcWi1d5ZuAUS3KFpump")).toBe("GX1J…pump");
  });
});
