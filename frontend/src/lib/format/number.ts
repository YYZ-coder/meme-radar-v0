// Display-only formatting of API decimal strings. No arithmetic on amounts beyond
// what is needed to render them (docs/engineering/14-frontend.md §4).
import type { Currency } from "@/lib/api/types";

export type Locale = "zh-CN" | "en";
export const UNKNOWN = "--";

const subscriptDigits = "₀₁₂₃₄₅₆₇₈₉";

function toSubscript(n: number) {
  return String(n)
    .split("")
    .map((d) => subscriptDigits[Number(d)])
    .join("");
}

function trimZeros(text: string) {
  return text.includes(".") ? text.replace(/\.?0+$/, "") : text;
}

function compact(abs: number, locale: Locale): string {
  if (locale === "zh-CN") {
    if (abs >= 1e8) return `${(abs / 1e8).toFixed(2)}亿`;
    if (abs >= 1e4) return `${(abs / 1e4).toFixed(2)}万`;
  } else {
    if (abs >= 1e9) return `${(abs / 1e9).toFixed(2)}B`;
    if (abs >= 1e6) return `${(abs / 1e6).toFixed(2)}M`;
    if (abs >= 1e3) return `${(abs / 1e3).toFixed(2)}K`;
  }
  return abs.toLocaleString("en-US", { maximumFractionDigits: 2 });
}

function affix(currency: Currency) {
  return currency === "USD" ? { pre: "$", post: "" } : { pre: "", post: ` ${currency}` };
}

/** Compact amount: $2840.00万 / $28.40M / 12.5 SOL. null → "--". */
export function formatAmount(
  value: string | null | undefined,
  locale: Locale,
  currency: Currency = "USD",
  { signed = false }: { signed?: boolean } = {}
): string {
  if (value === null || value === undefined) return UNKNOWN;
  const n = Number(value);
  if (!Number.isFinite(n)) return UNKNOWN;
  const { pre, post } = affix(currency);
  const sign = n < 0 ? "-" : signed && n > 0 ? "+" : "";
  return `${sign}${pre}${compact(Math.abs(n), locale)}${post}`;
}

/**
 * Price with subscript zeros: 0.00000412 → 0.0₅412.
 * Works on the decimal string so tiny prices keep their digits.
 */
export function formatPrice(value: string | null | undefined, currency: Currency = "USD"): string {
  if (value === null || value === undefined || !/^-?\d+(\.\d+)?$/.test(value)) return UNKNOWN;
  const { pre, post } = affix(currency);
  const negative = value.startsWith("-");
  const [intPart, frac = ""] = value.replace("-", "").split(".");
  const sign = negative ? "-" : "";
  if (Number(intPart) === 0 && frac) {
    const zeros = frac.match(/^0*/)?.[0].length ?? 0;
    const digits = frac.slice(zeros, zeros + 4).replace(/0+$/, "") || "0";
    if (zeros >= 4) return `${sign}${pre}0.0${toSubscript(zeros)}${digits}${post}`;
    return `${sign}${pre}${trimZeros(`0.${frac.slice(0, zeros + 4)}`)}${post}`;
  }
  const n = Number(value.replace("-", ""));
  const decimals = n >= 100 ? 2 : n >= 1 ? 4 : 6;
  const text = n.toLocaleString("en-US", { maximumFractionDigits: decimals });
  return `${sign}${pre}${text}${post}`;
}

/** Percent from a percent-number string ("24.68" → "+24.68%"). */
export function formatPercent(
  value: string | null | undefined,
  { signed = true }: { signed?: boolean } = {}
): string {
  if (value === null || value === undefined) return UNKNOWN;
  const n = Number(value);
  if (!Number.isFinite(n)) return UNKNOWN;
  const sign = n > 0 && signed ? "+" : "";
  return `${sign}${n.toFixed(2)}%`;
}

export function formatCount(value: number | null | undefined, locale: Locale): string {
  if (value === null || value === undefined) return UNKNOWN;
  return value.toLocaleString(locale === "zh-CN" ? "zh-CN" : "en-US");
}

/** Token quantity, compact for large values. */
export function formatQuantity(value: string | null | undefined, locale: Locale): string {
  if (value === null || value === undefined) return UNKNOWN;
  const n = Number(value);
  if (!Number.isFinite(n)) return UNKNOWN;
  const abs = Math.abs(n);
  if (abs > 0 && abs < 0.0001) return formatPrice(value, "USD").replace("$", "");
  return `${n < 0 ? "-" : ""}${compact(abs, locale)}`;
}

/** Sign of a decimal string: 1, -1 or 0; null when unknown. */
export function signOf(value: string | null | undefined): 1 | -1 | 0 | null {
  if (value === null || value === undefined) return null;
  const n = Number(value);
  if (!Number.isFinite(n)) return null;
  return n > 0 ? 1 : n < 0 ? -1 : 0;
}

export function toneOf(value: string | null | undefined): "positive" | "negative" | "" {
  const s = signOf(value);
  return s === 1 ? "positive" : s === -1 ? "negative" : "";
}
