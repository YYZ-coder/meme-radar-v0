"use client";
import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "./client";
import { nativePricesSchema, searchSchema } from "./schemas";

export function useNativePrices() {
  return useQuery({
    queryKey: ["market", "natives"],
    queryFn: ({ signal }) => apiFetch("/market/natives", nativePricesSchema, { signal }),
    refetchInterval: 30_000,
  });
}

export function useSearch(q: string) {
  const trimmed = q.trim();
  return useQuery({
    queryKey: ["search", trimmed],
    queryFn: ({ signal }) =>
      apiFetch("/search", searchSchema, { signal, query: { q: trimmed, limit: 8 } }),
    enabled: trimmed.length > 0,
    staleTime: 30_000,
  });
}
