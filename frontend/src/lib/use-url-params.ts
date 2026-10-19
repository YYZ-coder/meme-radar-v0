"use client";
import { useSearchParams } from "next/navigation";
import { useCallback } from "react";

/**
 * Update query params in place. Next.js syncs history.replaceState with useSearchParams,
 * so filters change without a server round trip and stay shareable.
 */
export function useUrlParams() {
  const params = useSearchParams();
  const update = useCallback(
    (patch: Record<string, string>, mode: "replace" | "push" = "replace") => {
      const next = new URLSearchParams(window.location.search);
      for (const [key, value] of Object.entries(patch)) {
        if (value) next.set(key, value);
        else next.delete(key);
      }
      const query = next.toString();
      const url = `${window.location.pathname}${query ? `?${query}` : ""}`;
      if (mode === "push") window.history.pushState(null, "", url);
      else window.history.replaceState(null, "", url);
    },
    []
  );
  return [params, update] as const;
}
