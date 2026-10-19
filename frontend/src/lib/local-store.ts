// Tiny localStorage-backed stores read through useSyncExternalStore (no hydration mismatch:
// the server snapshot is the default value).
import { useSyncExternalStore } from "react";

export function createLocalStore<T>(key: string, fallback: T, parse: (raw: unknown) => T) {
  const event = `radar-store:${key}`;
  let cachedRaw: string | null | undefined;
  let cachedValue = fallback;

  function read(): T {
    let raw: string | null = null;
    try {
      raw = localStorage.getItem(key);
    } catch {
      raw = null;
    }
    if (raw !== cachedRaw) {
      cachedRaw = raw;
      try {
        cachedValue = raw === null ? fallback : parse(JSON.parse(raw));
      } catch {
        cachedValue = fallback;
      }
    }
    return cachedValue;
  }

  function subscribe(callback: () => void) {
    const onStorage = (e: StorageEvent) => {
      if (e.key === key) callback();
    };
    window.addEventListener("storage", onStorage);
    window.addEventListener(event, callback);
    return () => {
      window.removeEventListener("storage", onStorage);
      window.removeEventListener(event, callback);
    };
  }

  /** Returns false when storage is unavailable (private mode, quota). */
  function write(value: T): boolean {
    try {
      localStorage.setItem(key, JSON.stringify(value));
      window.dispatchEvent(new Event(event));
      return true;
    } catch {
      return false;
    }
  }

  function useValue(): T {
    return useSyncExternalStore(subscribe, read, () => fallback);
  }

  return { read, write, useValue };
}
