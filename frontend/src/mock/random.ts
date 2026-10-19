// Deterministic pseudo-random helpers so mock data is stable across requests and tests.
import { hash } from "@/lib/hash";

export { hash };

export function rng(seed: number) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Uniform [0,1) for a key, without keeping generator state. */
export function unit(key: string): number {
  return rng(hash(key))();
}

const base58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz";

export function solanaAddress(key: string, suffix = ""): string {
  const r = rng(hash(`sol:${key}`));
  let out = "";
  const length = 44 - suffix.length;
  for (let i = 0; i < length; i++) out += base58[Math.floor(r() * 58)];
  return out + suffix;
}

export function evmAddress(key: string, suffix = ""): string {
  const r = rng(hash(`evm:${key}`));
  let out = "0x";
  for (let i = 0; i < 40 - suffix.length; i++) out += "0123456789abcdef"[Math.floor(r() * 16)];
  return out + suffix;
}

export function txHash(chain: "solana" | "base" | "bsc", key: string): string {
  if (chain === "solana") return solanaAddress(`tx:${key}`) + solanaAddress(`tx2:${key}`);
  const r = rng(hash(`txh:${key}`));
  let out = "0x";
  for (let i = 0; i < 64; i++) out += "0123456789abcdef"[Math.floor(r() * 16)];
  return out;
}

/** Decimal string with up to `digits` significant digits, never exponent notation. */
export function dec(value: number, digits = 6): string {
  if (!Number.isFinite(value)) return "0";
  if (value === 0) return "0";
  const abs = Math.abs(value);
  const magnitude = Math.floor(Math.log10(abs));
  const decimals = Math.max(0, Math.min(20, digits - 1 - magnitude));
  const text = abs.toFixed(decimals);
  const trimmed = text.includes(".") ? text.replace(/\.?0+$/, "") : text;
  return (value < 0 ? "-" : "") + trimmed;
}
