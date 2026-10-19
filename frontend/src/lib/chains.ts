import type { Chain } from "@/lib/api/types";

export const chains = ["solana", "base", "bsc"] as const satisfies readonly Chain[];

type ChainMeta = {
  name: string;
  native: "SOL" | "ETH" | "BNB";
  explorer: string;
  tokenPath: string;
  accountPath: string;
  txPath: string;
};

export const chainMeta: Record<Chain, ChainMeta> = {
  solana: {
    name: "Solana",
    native: "SOL",
    explorer: "https://solscan.io",
    tokenPath: "/token/",
    accountPath: "/account/",
    txPath: "/tx/",
  },
  base: {
    name: "Base",
    native: "ETH",
    explorer: "https://basescan.org",
    tokenPath: "/token/",
    accountPath: "/address/",
    txPath: "/tx/",
  },
  bsc: {
    name: "BSC",
    native: "BNB",
    explorer: "https://bscscan.com",
    tokenPath: "/token/",
    accountPath: "/address/",
    txPath: "/tx/",
  },
};

export function isChain(value: unknown): value is Chain {
  return typeof value === "string" && (chains as readonly string[]).includes(value);
}

const base58 = /^[1-9A-HJ-NP-Za-km-z]{32,44}$/;
const evm = /^0x[0-9a-fA-F]{40}$/;

/** Format check only (not a checksum check); the API is the source of truth. */
export function isAddress(chain: Chain, value: string): boolean {
  return chain === "solana" ? base58.test(value) : evm.test(value);
}

export function explorerUrl(chain: Chain, kind: "token" | "account" | "tx", value: string) {
  const meta = chainMeta[chain];
  const path =
    kind === "token" ? meta.tokenPath : kind === "account" ? meta.accountPath : meta.txPath;
  return `${meta.explorer}${path}${value}`;
}

export function tokenKey(token: { chain: Chain; address: string }) {
  return `${token.chain}:${token.address}`;
}

export function parseTokenKey(key: string): { chain: Chain; address: string } | null {
  const [chain, address] = key.split(":");
  if (!isChain(chain) || !address || !isAddress(chain, address)) return null;
  return { chain, address };
}
