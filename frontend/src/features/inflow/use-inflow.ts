"use client";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { ApiError, apiFetch, isApiError } from "@/lib/api/client";
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
  onExpired: () => void,
  refreshEmpty = true
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
    // Cold-start data can arrive after the first successful empty snapshot.
    // Once rows exist, keep the snapshot and its ordering stable as before.
    refetchInterval: (current) =>
      refreshEmpty &&
      current.state.status === "success" &&
      current.state.data?.pages[0]?.items.length === 0
        ? 10_000
        : false,
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
  enabled: boolean,
  snapshot?: { queryId: string; listVersion: string }
) {
  const stable = useMemo(() => keys.slice(0, 100), [keys]);
  const [expired, setExpired] = useState(false);
  const [overlay, setOverlay] = useState<{
    version: string | null;
    asOf: string | null;
    rows: Map<string, InflowRow>;
    missing: Set<string>;
  }>(() => ({ version: null, asOf: null, rows: new Map(), missing: new Set() }));
  const refresh = useQuery({
    queryKey: [
      "batch",
      snapshot?.queryId,
      snapshot?.listVersion,
      window,
      currency,
      stable.map(tokenKey).join(","),
    ],
    queryFn: async ({ signal }) => {
      try {
        const data = await apiFetch("/tokens/batch", batchSchema, {
          method: "POST",
          signal,
          headers: { "content-type": "application/json" },
          body: JSON.stringify({
            window,
            currency,
            tokens: stable,
            query_id: snapshot?.queryId,
            list_version: snapshot?.listVersion,
          }),
        });
        if (signal.aborted) throw new DOMException("Aborted", "AbortError");
        if (snapshot && data.list_version !== snapshot.listVersion)
          throw new ApiError(410, "query_expired", "list version changed");
        setOverlay((previous) => {
          const rows =
            previous.version === data.list_version
              ? new Map(previous.rows)
              : new Map<string, InflowRow>();
          const missing =
            previous.version === data.list_version ? new Set(previous.missing) : new Set<string>();
          const returned = new Set(data.items.map((row) => tokenKey(row.token)));
          for (const key of stable) {
            const id = tokenKey(key);
            if (!returned.has(id)) missing.add(id);
          }
          for (const row of data.items) {
            const id = tokenKey(row.token);
            rows.set(id, row);
            missing.delete(id);
          }
          return { version: data.list_version, asOf: data.as_of, rows, missing };
        });
        return data;
      } catch (error) {
        if (isApiError(error, 410)) setExpired(true);
        throw error;
      }
    },
    enabled: enabled && !expired && stable.length > 0,
    refetchInterval: enabled && !expired ? 10_000 : false,
    refetchIntervalInBackground: false,
    staleTime: 9_000,
    retry: (count, error) => count < 1 && !isApiError(error, 410),
  });
  return { latest: overlay.rows, missing: overlay.missing, asOf: overlay.asOf, expired, refresh };
}

export function useFacets(chain: Chain | null, window: Window) {
  return useQuery({
    queryKey: ["facets", chain, window],
    queryFn: ({ signal }) =>
      apiFetch("/smart-money/inflow/facets", facetsSchema, { signal, query: { chain, window } }),
    staleTime: 60_000,
  });
}
