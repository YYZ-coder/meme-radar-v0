// Smart-money list file parsing and validation for the admin import (product/08-admin §1).
import type { ImportRow } from "@/lib/api/types";

export const MAX_BYTES = 5 * 1024 * 1024;
export const MAX_ROWS = 10_000;

const secretHeader = /(private|secret|mnemonic|seed|助记词|私钥)/i;
const secretValue = [
  /^(0x)?[0-9a-fA-F]{64}$/, // EVM private key
  /^[1-9A-HJ-NP-Za-km-z]{86,90}$/, // Solana 64-byte secret key in base58
  /^\[(\s*\d{1,3}\s*,){63}\s*\d{1,3}\s*\]$/, // Solana keypair JSON array
  /^([a-z]+\s+){11}[a-z]+$|^([a-z]+\s+){23}[a-z]+$/, // BIP-39 style phrase
];

export class ImportFileError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string
  ) {
    super(message);
  }
}

/** Minimal RFC 4180 CSV parser (quotes, escaped quotes, CRLF). */
export function parseCsv(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let quoted = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (quoted) {
      if (ch === '"' && text[i + 1] === '"') {
        cell += '"';
        i++;
      } else if (ch === '"') quoted = false;
      else cell += ch;
    } else if (ch === '"') quoted = true;
    else if (ch === ",") {
      row.push(cell);
      cell = "";
    } else if (ch === "\n" || ch === "\r") {
      if (ch === "\r" && text[i + 1] === "\n") i++;
      row.push(cell);
      rows.push(row);
      row = [];
      cell = "";
    } else cell += ch;
  }
  if (cell !== "" || row.length) {
    row.push(cell);
    rows.push(row);
  }
  return rows.filter((r) => r.some((c) => c.trim() !== ""));
}

type RawRecord = Record<string, unknown>;

export function readRecords(fileName: string, text: string): RawRecord[] {
  const trimmed = text.trim();
  if (fileName.toLowerCase().endsWith(".json") || trimmed.startsWith("[")) {
    let data: unknown;
    try {
      data = JSON.parse(trimmed);
    } catch {
      throw new ImportFileError(400, "invalid_file", "JSON parse error");
    }
    if (!Array.isArray(data)) throw new ImportFileError(400, "invalid_file", "JSON must be array");
    return data.map((item) => (item && typeof item === "object" ? (item as RawRecord) : {}));
  }
  const [header, ...body] = parseCsv(text);
  if (!header) throw new ImportFileError(400, "invalid_file", "empty file");
  const names = header.map((h) => h.trim().toLowerCase());
  return body.map((cells) => Object.fromEntries(names.map((n, i) => [n, cells[i] ?? ""])));
}

function str(value: unknown): string | null {
  if (value === null || value === undefined) return null;
  const s = String(value).trim();
  return s === "" ? null : s;
}

export function containsSecret(records: RawRecord[]): boolean {
  for (const record of records) {
    for (const [k, v] of Object.entries(record)) {
      if (secretHeader.test(k)) return true;
      const s = typeof v === "string" ? v.trim() : Array.isArray(v) ? JSON.stringify(v) : "";
      if (s && secretValue.some((re) => re.test(s))) return true;
    }
  }
  return false;
}

const solana = /^[1-9A-HJ-NP-Za-km-z]{32,44}$/;
const evm = /^0x[0-9a-fA-F]{40}$/;
const twitter = /^(@?[A-Za-z0-9_]{1,15}|https:\/\/(x|twitter)\.com\/[A-Za-z0-9_]{1,15})$/;

export function validateRecords(
  records: RawRecord[],
  existing: (chain: string, address: string) => boolean
): ImportRow[] {
  if (records.length > MAX_ROWS) {
    throw new ImportFileError(413, "too_many_rows", `more than ${MAX_ROWS} rows`);
  }
  if (containsSecret(records)) {
    throw new ImportFileError(400, "secret_detected", "file looks like it contains keys");
  }
  const seen = new Set<string>();
  return records.map((record, index) => {
    const chain = str(record.chain)?.toLowerCase() ?? null;
    const address = str(record.address);
    const tagsRaw = record.tags;
    const tags = Array.isArray(tagsRaw)
      ? tagsRaw.map(String)
      : (str(tagsRaw)?.split(/[|;]/) ?? []).map((t) => t.trim()).filter(Boolean);
    const row: ImportRow = {
      line: index + 2,
      chain,
      address,
      label: str(record.label),
      tags,
      twitter: str(record.twitter),
      source: str(record.source),
      status: "valid",
      error: null,
    };
    const fail = (error: string): ImportRow => ({ ...row, status: "invalid", error });
    if (chain !== "solana" && chain !== "base" && chain !== "bsc") return fail("chain");
    if (!address) return fail("address");
    if (chain === "solana" ? !solana.test(address) : !evm.test(address)) return fail("address");
    if (row.twitter && !twitter.test(row.twitter)) return fail("twitter");
    if (row.label && row.label.length > 64) return fail("label");
    const id = `${chain}:${chain === "solana" ? address : address.toLowerCase()}`;
    if (seen.has(id)) return { ...row, status: "duplicate", error: null };
    seen.add(id);
    if (existing(chain, address)) return { ...row, status: "existing" };
    return row;
  });
}
