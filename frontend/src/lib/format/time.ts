import { UNKNOWN } from "./number";

/** Compact token age: 40s, 5m, 23h39m, 12d, 3mo, 1y. */
export function formatAge(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || seconds < 0) return UNKNOWN;
  if (seconds < 60) return `${Math.floor(seconds)}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) {
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    return m ? `${h}h${m}m` : `${h}h`;
  }
  const days = Math.floor(seconds / 86400);
  if (days < 30) return `${days}d`;
  if (days < 365) return `${Math.floor(days / 30)}mo`;
  return `${Math.floor(days / 365)}y`;
}

/** Seconds elapsed between an ISO timestamp and now. */
export function secondsSince(iso: string | null | undefined, now = Date.now()): number | null {
  if (!iso) return null;
  const t = Date.parse(iso);
  return Number.isFinite(t) ? Math.max(0, Math.floor((now - t) / 1000)) : null;
}

/** Hover text with UTC and local time. */
export function timeTitle(iso: string | null | undefined): string | undefined {
  if (!iso) return undefined;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return undefined;
  return `UTC ${d.toISOString().replace("T", " ").slice(0, 19)} · ${d.toLocaleString()}`;
}

export function formatClock(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? UNKNOWN : d.toLocaleTimeString("en-GB", { hour12: false });
}
