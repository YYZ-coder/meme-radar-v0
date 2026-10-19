// In-memory admin state for the mock: list versions, members, exclusions, imports, failures.
import type {
  AdminHealth,
  Chain,
  ImportReport,
  ImportRow,
  ListVersionChange,
  Member,
  ParseFailure,
} from "@/lib/api/types";
import { listVersionRef } from "./inflow";
import { evmAddress, rng, hash, solanaAddress } from "./random";

const START = Date.parse("2026-10-18T00:00:00Z");

function seedMembers(): Member[] {
  const r = rng(hash("members"));
  const names = ["Alpha Hunter", "Sol Sniper", "Meme Explorer", "Degen Research", "pixelwhale"];
  return Array.from({ length: 36 }, (_, i) => {
    const chain: Chain = i % 3 === 0 ? "solana" : i % 3 === 1 ? "base" : "bsc";
    const imported = i < 12;
    return {
      chain,
      address: chain === "solana" ? solanaAddress(`sm${i}`) : evmAddress(`sm${i}`),
      label: imported && i < names.length ? names[i] : null,
      tags: imported && r() < 0.5 ? ["kol"] : [],
      twitter: imported && i < names.length ? `@${names[i].replace(/\s/g, "")}` : null,
      sources: imported ? (i % 4 === 0 ? ["import", "auto"] : ["import"]) : ["auto"],
      added_at: new Date(START - i * 3_600_000).toISOString(),
      excluded: false,
    };
  });
}

export const adminState = {
  members: seedMembers(),
  exclusions: [] as { chain: Chain; address: string; reason: string; created_at: string }[],
  /** Reports keep every row in `all`; the API response returns at most 500. */
  imports: new Map<string, { report: ImportReport; all: ImportRow[] }>(),
  failures: [
    failure(
      "f1",
      "solana",
      "pumpswap",
      "457713912:118:2",
      "conservation check failed: quote delta"
    ),
    failure("f2", "base", "uniswap_v4", "52690612:41:0", "unknown hook 0x…a8c0 on pool"),
    failure("f3", "bsc", "four_meme", "128015230:7:1", "decode: unknown event topic"),
  ] as ParseFailure[],
};

function failure(id: string, chain: Chain, module: string, position: string, error: string) {
  return {
    id,
    chain,
    module,
    position,
    error,
    attempts: 3,
    status: "failed" as const,
    last_attempt_at: new Date(START + 3_600_000).toISOString(),
  };
}

export function memberId(chain: string, address: string) {
  return `${chain}:${chain === "solana" ? address : address.toLowerCase()}`;
}

export function isMember(chain: string, address: string) {
  const id = memberId(chain, address);
  return adminState.members.some((m) => memberId(m.chain, m.address) === id);
}

export function bumpVersion(change: Omit<ListVersionChange, "list_version" | "previous_version">) {
  const previous = String(listVersionRef.current);
  listVersionRef.current += 1;
  return { list_version: String(listVersionRef.current), previous_version: previous, ...change };
}

const anchors: Record<Chain, { anchor: number; perSecond: number }> = {
  solana: { anchor: 457_713_550, perSecond: 2.5 },
  base: { anchor: 52_690_400, perSecond: 0.5 },
  bsc: { anchor: 128_014_990, perSecond: 1.33 },
};
const ANCHOR_AT = Date.parse("2026-10-17T00:00:00Z");

export function health(now: number): AdminHealth {
  const chains = (Object.keys(anchors) as Chain[]).map((chain, i) => {
    const { anchor, perSecond } = anchors[chain];
    const elapsed = (now - ANCHOR_AT) / 1000;
    const head = Math.floor(anchor + elapsed * perSecond);
    const lowerBound = Math.floor(anchor - 30 * 86400 * perSecond);
    const lag = [6, 14, 41][i];
    const history = Math.floor(
      anchor - Math.min(elapsed * perSecond * [0.6, 2.2, 1.1][i], anchor - lowerBound)
    );
    return {
      chain,
      head,
      anchor,
      lower_bound: lowerBound,
      forward_position: head - Math.round(lag * perSecond),
      history_position: history,
      backlog_blocks: Math.round(lag * perSecond),
      lag_seconds: lag,
      gaps_open: [2, 0, 5][i],
      gaps_by_reason: (
        [{ history_unavailable: 2 }, {}, { retryable: 3, budget_paused: 2 }] as Record<
          string,
          number
        >[]
      )[i],
      last_error:
        i === 2
          ? {
              kind: "retryable" as const,
              message: "429 Too Many Requests from public RPC",
              at: new Date(now - 95_000).toISOString(),
            }
          : null,
      budget_paused: false,
    };
  });
  const parsers = [
    ["solana", "pump", "1.0.0"],
    ["solana", "pumpswap", "1.0.0"],
    ["solana", "raydium_launchlab", "1.0.0"],
    ["solana", "jupiter", "1.0.0"],
    ["base", "clanker", "1.0.0"],
    ["base", "uniswap_v4", "1.0.0"],
    ["bsc", "four_meme", "1.0.0"],
    ["bsc", "pancakeswap", "1.0.0"],
  ].map(([chain, module, version], i) => {
    const c = chains.find((x) => x.chain === chain)!;
    return {
      chain: chain as Chain,
      module,
      version,
      position: (c.forward_position ?? 0) - i * 3,
      lag_blocks: i * 3,
      quarantined: [4, 9, 0, 0, 1, 2, 3, 0][i],
      failures: adminState.failures.filter((f) => f.module === module && f.status === "failed")
        .length,
      unknown_discriminators: [0, 1, 0, 6, 0, 0, 2, 0][i],
    };
  });
  return {
    as_of: new Date(now).toISOString(),
    chains,
    parsers,
    failures: adminState.failures,
    budget: {
      month: new Date(now).toISOString().slice(0, 7),
      spent_usd: "0",
      limit_usd: "100",
      state: "normal",
    },
  };
}
