// Browser-only watchlist until account follows arrive in M4 (D97/D98).
import { parseTokenKey } from "./chains";
import { createLocalStore } from "./local-store";

const MAX = 100; // POST /tokens/batch accepts at most 100 tokens

const store = createLocalStore<string[]>("meme-radar:watchlist:v2", [], (raw) =>
  Array.isArray(raw)
    ? raw
        .filter((k): k is string => typeof k === "string" && parseTokenKey(k) !== null)
        .slice(0, MAX)
    : []
);

export function useWatchlist() {
  const keys = store.useValue();
  const toggle = (key: string): boolean => {
    const current = store.read();
    const next = current.includes(key)
      ? current.filter((k) => k !== key)
      : [key, ...current].slice(0, MAX);
    return store.write(next);
  };
  return { keys, has: (key: string) => keys.includes(key), toggle };
}
