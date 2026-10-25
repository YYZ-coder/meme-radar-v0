import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import type { InflowRow } from "@/lib/api/types";
import { tokenKey } from "@/lib/chains";
import { useInflowList, useRowRefresh } from "./use-inflow";
const request = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  apiFetch: request,
}));
afterEach(() => {
  vi.clearAllMocks();
  vi.useRealTimers();
});
const row: InflowRow = {
  token: { chain: "base", address: "0x" + "01".repeat(20), symbol: "X", name: "X" },
  currency: "USD",
  price: "7",
  market_cap: null,
  price_change_pct: null,
  liquidity: null,
  trades: 1,
  buys: 1,
  sells: 0,
  observed: { trades: 1, buys: 1, sells: 0 },
  coverage: "complete",
  volume: null,
  net_inflow: null,
  sm_holders: null,
  sm_trades: null,
  holders: null,
  age_seconds: null,
  dexes: [],
  socials: {},
  sparkline: [],
};
it("refreshes an empty cold-start snapshot, respects pause, then preserves populated ordering", async () => {
  vi.useFakeTimers();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const response = (items: InflowRow[], id: string) => ({
    query_id: id,
    list_version: "42",
    as_of: "2026-10-20T10:00:00Z",
    window: "1h",
    total: items.length,
    truncated: false,
    offset: 0,
    next_offset: null,
    items,
  });
  request
    .mockResolvedValueOnce(response([], "empty"))
    .mockResolvedValueOnce(response([row], "ready"));
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const apiQuery = { window: "1h" };
  const expired = vi.fn();
  const { result, rerender, unmount } = renderHook(
    ({ refresh }) => useInflowList(apiQuery, 0, expired, refresh),
    { wrapper, initialProps: { refresh: false } }
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10);
  });
  expect(result.current.first?.query_id).toBe("empty");
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10_000);
  });
  expect(request).toHaveBeenCalledTimes(1);
  rerender({ refresh: true });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10_010);
  });
  expect(result.current.rows).toEqual([row]);
  expect(result.current.first?.query_id).toBe("ready");
  await act(async () => {
    await vi.advanceTimersByTimeAsync(20_000);
  });
  expect(request).toHaveBeenCalledTimes(2);
  expect(expired).not.toHaveBeenCalled();
  unmount();
  client.clear();
});
it("keeps the last successful value and marks a requested token missing, then clears it on recovery", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const response = (items: InflowRow[]) => ({
    list_version: "42",
    as_of: "2026-10-20T10:00:00Z",
    items,
  });
  request.mockResolvedValueOnce(response([row]));
  const keys = [row.token];
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const { result, unmount } = renderHook(
    () => useRowRefresh(keys, "1h", "usd", true, { queryId: "fixed", listVersion: "42" }),
    { wrapper }
  );
  const id = tokenKey(row.token);
  await waitFor(() => expect(result.current.latest.get(id)?.price).toBe("7"));
  request.mockResolvedValueOnce(response([]));
  await act(async () => {
    await result.current.refresh.refetch();
  });
  expect(result.current.latest.get(id)?.price).toBe("7");
  expect(result.current.missing.has(id)).toBe(true);
  request.mockResolvedValueOnce(response([{ ...row, price: "8" }]));
  await act(async () => {
    await result.current.refresh.refetch();
  });
  expect(result.current.latest.get(id)?.price).toBe("8");
  expect(result.current.missing.has(id)).toBe(false);
  unmount();
  client.clear();
});
