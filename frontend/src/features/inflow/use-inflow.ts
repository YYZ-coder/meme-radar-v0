"use client";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef } from "react";
import { apiFetch, isApiError } from "@/lib/api/client";
import { batchSchema, facetsSchema, inflowPageSchema } from "@/lib/api/schemas";
import type { Chain, InflowRow, Window } from "@/lib/api/types";
import { tokenKey } from "@/lib/chains";

type PageParam = { queryId: string; offset: number } | null;

/**
 * Snapshot list (TECHNICAL_PLAN §3.4): first page creates query_id, later pages only send
 * query_id + offset. A 410 while paging calls onExpired once; the caller re-mounts the list
 * with a new generation so it re-queries from the URL state (Demo 08 §4).
 */
export function useInflowList(
  apiQuery: Record<string, string>,
  generation: number,
  onExpired: () => void
) {
  const queryKey = useMemo(() => ["inflow", apiQuery, generation] as const, [apiQuery, generation]);
  const query = useInfiniteQuery({
    queryKey,
    initialPageParam: null as PageParam,
    queryFn: ({ pageParam, signal }) =>
      pageParam
        ? apiFetch("/smart-money/inflow", inflowPageSchema, {
            signal,
            query: { query_id: pageParam.queryId, offset: pageParam.offset, limit: 50 },
          })
        : apiFetch("/smart-money/inflow", inflowPageSchema, {
            signal,
            query: { ...apiQuery, limit: 50 },
          }),
    getNextPageParam: (last): PageParam | undefined =>
      last.next_offset === null ? undefined : { queryId: last.query_id, offset: last.next_offset },
    staleTime: Infinity,
    gcTime: 60_000,
    // 410 / 429 / 503 / 4xx are handled by the UI; only network errors and other 5xx retry once.
    retry: (count, error) =>
      count < 1 &&
      (!isApiError(error) || error.status === 0 || (error.status >= 500 && error.status !== 503)),
  });

  const expired = query.isFetchNextPageError && isApiError(query.error, 410);
  const handled = useRef(false);
  useEffect(() => {
    if (expired && !handled.current) {
      handled.current = true;
      onExpired();
    }
  }, [expired, onExpired]);

  const rows = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data]);
  const first = query.data?.pages[0];
  return { query, rows, first };
}

/** Values-only refresh for the given keys every 10 s; never changes row order. */
export function useRowRefresh(
  keys: { chain: Chain; address: string }[],
  window: Window,
  currency: "usd" | "native",
  enabled: boolean
) {
  const stable = useMemo(() => keys.slice(0, 100), [keys]);
  const refresh = useQuery({
    queryKey: ["batch", window, currency, stable.map(tokenKey).join(",")],
    queryFn: ({ signal }) =>
      apiFetch("/tokens/batch", batchSchema, {
        method: "POST",
        signal,
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ window, currency, tokens: stable }),
      }),
    enabled: enabled && stable.length > 0,
    refetchInterval: enabled ? 10_000 : false,
    staleTime: 9_000,
  });
  const latest = useMemo(() => {
    const map = new Map<string, InflowRow>();
    for (const row of refresh.data?.items ?? []) map.set(tokenKey(row.token), row);
    return map;
  }, [refresh.data]);
  return { latest, asOf: refresh.data?.as_of ?? null, refresh };
}

export function useFacets(chain: Chain | null, window: Window) {
  return useQuery({
    queryKey: ["facets", chain, window],
    queryFn: ({ signal }) =>
      apiFetch("/smart-money/inflow/facets", facetsSchema, { signal, query: { chain, window } }),
    staleTime: 60_000,
  });
}
