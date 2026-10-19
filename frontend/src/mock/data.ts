// Mock token universe for the M1 frontend. Real sample tokens from docs/demos/samples are
// included; everything else is generated ("构造") and flagged by the x-radar-mock header.
import type { Chain, InflowRow, Window } from "@/lib/api/types";
import { dec, evmAddress, hash, rng, solanaAddress, unit } from "./random";

export type Platform = { id: string; name: string; chain: Chain; curve: boolean };
export type Dex = { id: string; name: string; chain: Chain; protocol: string; quote: string };

export const platforms: Platform[] = [
  { id: "pump_fun", name: "Pump.fun", chain: "solana", curve: true },
  { id: "raydium_launchlab", name: "Raydium LaunchLab", chain: "solana", curve: true },
  { id: "clanker", name: "Clanker", chain: "base", curve: false },
  { id: "four_meme", name: "Four.meme", chain: "bsc", curve: true },
];

export const dexes: Dex[] = [
  { id: "pumpswap", name: "PumpSwap", chain: "solana", protocol: "pumpswap", quote: "SOL" },
  { id: "pump_curve", name: "Pump", chain: "solana", protocol: "pump_curve", quote: "SOL" },
  {
    id: "raydium_cpmm",
    name: "Raydium CPMM",
    chain: "solana",
    protocol: "raydium_cpmm",
    quote: "SOL",
  },
  {
    id: "launchlab_curve",
    name: "LaunchLab",
    chain: "solana",
    protocol: "raydium_launchlab",
    quote: "SOL",
  },
  { id: "uniswap_v4", name: "Uniswap v4", chain: "base", protocol: "uniswap_v4", quote: "ETH" },
  {
    id: "pancakeswap_v2",
    name: "PancakeSwap v2",
    chain: "bsc",
    protocol: "pancakeswap_v2",
    quote: "BNB",
  },
  {
    id: "four_meme_curve",
    name: "Four.meme",
    chain: "bsc",
    protocol: "four_meme",
    quote: "BNB",
  },
];

export const nativeUsd: Record<Chain, number> = { solana: 105.247, base: 2454.07, bsc: 729.4 };
export const nativeSymbol: Record<Chain, "SOL" | "ETH" | "BNB"> = {
  solana: "SOL",
  base: "ETH",
  bsc: "BNB",
};

export type MockToken = {
  chain: Chain;
  address: string;
  symbol: string;
  name: string;
  platform: string | null;
  dexes: string[];
  graduated: boolean;
  launchAt: number;
  supply: number;
  decimals: number;
  basePrice: number | null;
  baseLiquidity: number | null;
  tradesPerHour: number;
  smShare: number;
  smHolders: number;
  holders: number;
  socials: { twitter?: string; telegram?: string; website?: string; github?: string };
  creator: string;
  devHoldingPct: number | null;
  seed: number;
  sample: boolean;
};

const DAY = 86400_000;
/** Fixed reference time so generated launches are stable; ages move with the clock. */
const EPOCH = Date.parse("2026-10-19T00:00:00Z");

const words = [
  "PEPE",
  "WIF",
  "BONK",
  "POPCAT",
  "MOODENG",
  "BRETT",
  "TOSHI",
  "ANDY",
  "FLOKI",
  "BANANA",
  "GIGA",
  "MEW",
  "PONKE",
  "SLERF",
  "MOTHER",
  "DEGEN",
  "HIGHER",
  "KEYCAT",
  "MIGGLES",
  "SKI",
  "CHILLGUY",
  "GOAT",
  "PNUT",
  "FWOG",
  "MICHI",
  "RETARDIO",
  "LOCKIN",
  "SPX",
  "HOSICO",
  "BOME",
  "NEIRO",
  "TURBO",
  "DOGINME",
  "BENJI",
  "TYBG",
  "BASED",
  "ROCKY",
  "CATE",
  "BABYDOGE",
  "TST",
  "MUBARAK",
  "BROCCOLI",
  "CHEEMS",
  "SIREN",
  "TUT",
  "BANK",
  "FORM",
  "PIXEL",
  "RADAR",
  "ALPHA",
];
const suffixes = ["", "", "", " INU", " AI", " CAT", " 2.0", " DAO", " ON SOL", " CLUB"];

function seedTokens(): MockToken[] {
  const now = EPOCH;
  return [
    {
      chain: "solana",
      address: "GX1JGwFmrzgkv6MBjwZZdZt3qBcWi1d5ZuAUS3KFpump",
      symbol: "GX1J",
      name: "GX1J (sample)",
      platform: "pump_fun",
      dexes: ["pump_curve"],
      graduated: false,
      launchAt: now - 3 * 3600_000,
      supply: 955336024.2689,
      decimals: 6,
      basePrice: 0.0000322262,
      baseLiquidity: 14582.94,
      tradesPerHour: 380,
      smShare: 0.06,
      smHolders: 7,
      holders: 412,
      socials: { twitter: "https://x.com/" },
      creator: "4v9uPGeHMooSQiL2rV1LeN4qN5Li5c1TNCh6JmkvBuu6",
      devHoldingPct: 2.4,
      seed: hash("GX1J"),
      sample: true,
    },
    {
      chain: "solana",
      address: "HWgMx9sQqk5zHBtjJcfGRskKkwfx927Q2XHvtxg9pump",
      symbol: "HWGMX",
      name: "HWgMx (sample)",
      platform: "pump_fun",
      dexes: ["pump_curve"],
      graduated: false,
      launchAt: now - 40 * 60_000,
      supply: 1_000_000_000,
      decimals: 6,
      basePrice: null,
      baseLiquidity: null,
      tradesPerHour: 260,
      smShare: 0.08,
      smHolders: 3,
      holders: 96,
      socials: {},
      creator: solanaAddress("creator-hwgmx"),
      devHoldingPct: null,
      seed: hash("HWGMX"),
      sample: true,
    },
    {
      chain: "solana",
      address: "EvJBc8govNsZ4fi5SSKp3BPXg9AVyssipm16c2GPbg4e",
      symbol: "EVJB",
      name: "EvJB (sample)",
      platform: "pump_fun",
      dexes: ["pumpswap"],
      graduated: true,
      launchAt: now - 9 * DAY,
      supply: 1_000_000_000,
      decimals: 6,
      basePrice: 0.0000171136,
      baseLiquidity: 24832.65,
      tradesPerHour: 140,
      smShare: 0.05,
      smHolders: 4,
      holders: 1288,
      socials: { twitter: "https://x.com/", telegram: "https://t.me/" },
      creator: solanaAddress("creator-evjb"),
      devHoldingPct: 0,
      seed: hash("EVJB"),
      sample: true,
    },
    {
      chain: "base",
      address: "0x5b7c19533b7f62c980cecd475ba50018c32f38d3",
      symbol: "NODF",
      name: "NODF",
      platform: "clanker",
      dexes: ["uniswap_v4"],
      graduated: true,
      launchAt: now - 21 * DAY,
      supply: 10_000_000_000,
      decimals: 18,
      basePrice: 0.0085483593,
      baseLiquidity: 2_140_000,
      tradesPerHour: 210,
      smShare: 0.04,
      smHolders: 18,
      holders: 9120,
      socials: { twitter: "https://x.com/", website: "https://example.com" },
      creator: evmAddress("creator-nodf"),
      devHoldingPct: 1.2,
      seed: hash("NODF"),
      sample: true,
    },
    {
      chain: "base",
      address: "0x7d4bfd446d4b81d5ede62529f9c8db6fdd81e222",
      symbol: "RIBBIT",
      name: "Blue Frog",
      platform: "clanker",
      dexes: ["uniswap_v4"],
      graduated: true,
      launchAt: now - 63 * DAY,
      supply: 1_000_000_000,
      decimals: 18,
      basePrice: 0.0049,
      baseLiquidity: 860_000,
      tradesPerHour: 120,
      smShare: 0.05,
      smHolders: 11,
      holders: 4410,
      socials: { twitter: "https://x.com/" },
      creator: evmAddress("creator-ribbit"),
      devHoldingPct: 0,
      seed: hash("RIBBIT"),
      sample: true,
    },
    {
      chain: "bsc",
      address: "0xf657490f3c5563895ee06833cc54d43f13ce4444",
      symbol: "飞行汽车",
      name: "飞行汽车",
      platform: "four_meme",
      dexes: ["four_meme_curve"],
      graduated: false,
      launchAt: now - 5 * 3600_000,
      supply: 1_000_000_000,
      decimals: 18,
      basePrice: 0.000033592993324706,
      baseLiquidity: 18_420,
      tradesPerHour: 320,
      smShare: 0.07,
      smHolders: 5,
      holders: 640,
      socials: { telegram: "https://t.me/" },
      creator: evmAddress("creator-flycar"),
      devHoldingPct: 3.1,
      seed: hash("FLYCAR"),
      sample: true,
    },
    {
      chain: "bsc",
      address: "0x0e67e6dc4c512ebc8c11fd47f6a5445643ebffff",
      symbol: "sanctuary",
      name: "sanctuary",
      platform: "four_meme",
      dexes: ["four_meme_curve"],
      graduated: false,
      launchAt: now - 2 * 3600_000,
      supply: 1_000_000_000,
      decimals: 18,
      basePrice: 0.0000032097,
      baseLiquidity: 6_200,
      tradesPerHour: 160,
      smShare: 0.03,
      smHolders: 1,
      holders: 77,
      socials: {},
      creator: "0x47a38019f14983f5d1ef6b0fde2f31421b547647",
      devHoldingPct: 12.5,
      seed: hash("SANCTUARY"),
      sample: true,
    },
  ];
}

function generated(index: number): MockToken {
  const r = rng(hash(`token-${index}`));
  const chain: Chain = index % 10 < 6 ? "solana" : index % 10 < 8 ? "base" : "bsc";
  const word = words[index % words.length];
  const symbol = (word + (index >= words.length ? String(index % 7) : "")).slice(0, 12);
  const name = `${word[0]}${word.slice(1).toLowerCase()}${suffixes[Math.floor(r() * suffixes.length)]}`;
  const platformList = platforms.filter((p) => p.chain === chain);
  const platform = r() < 0.85 ? platformList[Math.floor(r() * platformList.length)] : null;
  const ageDays = r() < 0.35 ? r() * 1.5 : r() < 0.7 ? 1 + r() * 29 : 30 + r() * 400;
  const graduated = platform ? !platform.curve || r() < 0.55 || ageDays > 3 : true;
  const dexIds =
    chain === "solana"
      ? platform?.id === "raydium_launchlab"
        ? graduated
          ? ["raydium_cpmm"]
          : ["launchlab_curve"]
        : graduated
          ? r() < 0.25
            ? ["pumpswap", "raydium_cpmm"]
            : ["pumpswap"]
          : ["pump_curve"]
      : chain === "base"
        ? ["uniswap_v4"]
        : graduated
          ? ["pancakeswap_v2"]
          : ["four_meme_curve"];
  const mcap = Math.exp(Math.log(8_000) + r() * Math.log(80_000_000 / 8_000));
  const supply = chain === "base" && r() < 0.5 ? 100_000_000_000 : 1_000_000_000;
  const unknownPrice = r() < 0.04;
  const unknownLiquidity = unknownPrice || r() < 0.04;
  const holders = Math.round(40 + Math.sqrt(mcap) * (0.5 + r() * 2));
  const socials: MockToken["socials"] = {};
  if (r() < 0.75) socials.twitter = "https://x.com/";
  if (r() < 0.55) socials.telegram = "https://t.me/";
  if (r() < 0.4) socials.website = "https://example.com";
  if (r() < 0.08) socials.github = "https://github.com/";
  const address =
    chain === "solana"
      ? solanaAddress(`t${index}`, platform?.id === "pump_fun" ? "pump" : "")
      : evmAddress(`t${index}`, platform?.id === "four_meme" ? "4444" : "");
  return {
    chain,
    address,
    symbol,
    name,
    platform: platform?.id ?? null,
    dexes: dexIds,
    graduated,
    launchAt: EPOCH - ageDays * DAY,
    supply,
    decimals: chain === "solana" ? 6 : 18,
    basePrice: unknownPrice ? null : mcap / supply,
    baseLiquidity: unknownLiquidity ? null : mcap * (0.05 + r() * 0.12),
    tradesPerHour: Math.round(20 + Math.sqrt(mcap) * 0.15 * (0.3 + r() * 2)),
    smShare: 0.01 + r() * 0.08,
    smHolders: Math.round(r() * Math.min(60, Math.sqrt(holders))),
    holders,
    socials,
    creator: chain === "solana" ? solanaAddress(`creator${index}`) : evmAddress(`creator${index}`),
    devHoldingPct: r() < 0.2 ? null : r() < 0.4 ? 0 : Number((r() * 9).toFixed(2)),
    seed: hash(`seed-${index}`),
    sample: false,
  };
}

export const tokens: MockToken[] = [
  ...seedTokens(),
  ...Array.from({ length: 253 }, (_, i) => generated(i)),
];

const byKey = new Map(tokens.map((t) => [`${t.chain}:${t.address.toLowerCase()}`, t]));
export function findToken(chain: string, address: string): MockToken | undefined {
  return byKey.get(`${chain}:${address.toLowerCase()}`);
}

export const windowSeconds: Record<Window, number> = {
  "1m": 60,
  "5m": 300,
  "1h": 3600,
  "4h": 14400,
  "24h": 86400,
};

/** Smooth deterministic price path; null before launch or when price is unknown. */
export function priceAt(token: MockToken, ms: number): number | null {
  if (token.basePrice === null || ms < token.launchAt) return null;
  const t = ms / 1000;
  const s = token.seed % 1000;
  const wave =
    0.18 * Math.sin(t / 5400 + s) +
    0.07 * Math.sin(t / 900 + s * 2) +
    0.025 * Math.sin(t / 97 + s * 3) +
    0.01 * Math.sin(t / 13 + s);
  const sinceLaunch = Math.min(1, (ms - token.launchAt) / (6 * 3600_000));
  return token.basePrice * Math.exp(wave) * (0.35 + 0.65 * sinceLaunch);
}

/** Trades in a window, scaled by token activity with a little time jitter. */
function tradesIn(token: MockToken, seconds: number, now: number): number {
  if (now - token.launchAt < seconds * 1000) seconds = Math.max(0, (now - token.launchAt) / 1000);
  const bucket = Math.floor(now / 10_000);
  const jitter = 0.85 + 0.3 * unit(`${token.address}:${seconds}:${bucket}`);
  return Math.round((token.tradesPerHour * seconds * jitter) / 3600);
}

export type Metrics = {
  price: number | null;
  priceStart: number | null;
  marketCap: number | null;
  liquidity: number | null;
  trades: number;
  buys: number;
  sells: number;
  volume: number | null;
  netInflow: number | null;
  smTrades: number;
  smBuyVolume: number | null;
  smSellVolume: number | null;
  traders: number;
  sparkline: (number | null)[];
};

export function metrics(token: MockToken, window: Window, now: number): Metrics {
  const seconds = windowSeconds[window];
  const price = priceAt(token, now);
  const start = now - seconds * 1000;
  const priceStart = priceAt(token, Math.max(start, token.launchAt));
  const trades = tradesIn(token, seconds, now);
  const buyRatio = 0.45 + 0.25 * unit(`${token.address}:buy:${window}:${Math.floor(now / 60_000)}`);
  const buys = Math.round(trades * buyRatio);
  const avgTrade = price === null ? null : 40 + (token.supply * (token.basePrice ?? 0)) / 60_000;
  const volume = avgTrade === null ? null : trades * avgTrade;
  const smTrades = Math.round(trades * token.smShare);
  const smBias = unit(`${token.address}:sm:${window}:${Math.floor(now / 30_000)}`) - 0.38;
  const smVolume = volume === null ? null : volume * token.smShare * 1.6;
  const smBuy = smVolume === null ? null : smVolume * (0.5 + smBias / 2);
  const smSell = smVolume === null || smBuy === null ? null : smVolume - smBuy;
  const sparkline = Array.from({ length: 20 }, (_, i) =>
    priceAt(token, start + ((i + 1) * seconds * 1000) / 20)
  );
  return {
    price,
    priceStart,
    marketCap: price === null ? null : price * token.supply,
    liquidity:
      token.baseLiquidity === null || price === null || token.basePrice === null
        ? null
        : token.baseLiquidity * Math.sqrt(price / token.basePrice),
    trades,
    buys,
    sells: trades - buys,
    volume,
    netInflow: smBuy === null || smSell === null ? null : smBuy - smSell,
    smTrades,
    smBuyVolume: smBuy,
    smSellVolume: smSell,
    traders: Math.max(trades ? 1 : 0, Math.round(trades * 0.42)),
    sparkline,
  };
}

export function conv(value: number | null, chain: Chain, native: boolean): number | null {
  if (value === null) return null;
  return native ? value / nativeUsd[chain] : value;
}

export function tokenRef(token: MockToken) {
  return {
    chain: token.chain,
    address: token.address,
    symbol: token.symbol,
    name: token.name,
    image_url: null,
    launch_platform: platforms.find((p) => p.id === token.platform)?.name ?? null,
    launch_at: new Date(token.launchAt).toISOString(),
  };
}

export function inflowRow(
  token: MockToken,
  window: Window,
  native: boolean,
  now: number
): InflowRow {
  const m = metrics(token, window, now);
  const c = (v: number | null) => {
    const x = conv(v, token.chain, native);
    return x === null ? null : dec(x, 8);
  };
  const change =
    m.price === null || m.priceStart === null || m.priceStart === 0
      ? null
      : dec(((m.price - m.priceStart) / m.priceStart) * 100, 6);
  return {
    token: tokenRef(token),
    currency: native ? nativeSymbol[token.chain] : "USD",
    price: c(m.price),
    market_cap: c(m.marketCap),
    price_change_pct: change,
    liquidity: c(m.liquidity),
    trades: m.trades,
    buys: m.buys,
    sells: m.sells,
    volume: c(m.volume),
    net_inflow: c(m.netInflow),
    sm_holders: token.smHolders,
    sm_trades: m.smTrades,
    holders: token.holders,
    age_seconds: Math.max(0, Math.floor((now - token.launchAt) / 1000)),
    dexes: token.dexes.map((id) => dexes.find((d) => d.id === id)?.name ?? id),
    socials: token.socials,
    sparkline: m.sparkline.map((p) => c(p)),
  };
}
