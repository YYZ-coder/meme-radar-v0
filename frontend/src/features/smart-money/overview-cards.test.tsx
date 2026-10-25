import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, expect, it, vi } from "vitest";
import type { OverviewResponse } from "@/lib/api/types";
import en from "@/messages/en.json";
import zh from "@/messages/zh-CN.json";
import { OverviewCards } from "./overview-cards";

const request = vi.hoisted(() => vi.fn());
vi.mock("next/navigation", () => ({ useSearchParams: () => new URLSearchParams("window=1m") }));
vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  apiFetch: request,
}));
const clients: QueryClient[] = [];
afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
  vi.clearAllMocks();
});

const partial: OverviewResponse = {
  as_of: "2026-10-21T12:00:00Z",
  list_version: "1",
  window: "1m",
  coverage: "partial",
  net_inflow_usd: null,
  net_inflow_change_pct: null,
  active_wallets: null,
  tracked_tokens: null,
  new_tokens: null,
  buy_volume_usd: null,
  sell_volume_usd: null,
  buy_share_pct: null,
  large_buy_volume_usd: null,
  small_buy_volume_usd: null,
  large_buy_share_pct: null,
  small_buy_share_pct: null,
  inflow_bars: [null, null],
};

function show(data = partial, locale: "en" | "zh-CN" = "en") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  request.mockResolvedValue(data);
  render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale={locale} messages={locale === "en" ? en : zh}>
        <OverviewCards chain="base" paused />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

it.each([
  ["en", "Overview · incomplete coverage", "Tracked tokens"],
  ["zh-CN", "概览·覆盖不完整", "收录代币"],
] as const)("labels partial overview totals as unknown in %s", async (locale, hint, tracked) => {
  show(partial, locale);
  expect(await screen.findByRole("note")).toHaveTextContent(hint);
  const card = screen.getByText(tracked).closest("article");
  expect(card).not.toBeNull();
  expect(within(card!).getByText("--", { exact: true })).toBeVisible();
  expect(within(card!).queryByText("0", { exact: true })).not.toBeInTheDocument();
});

it("keeps complete count coverage even when USD metrics are unavailable", async () => {
  show({ ...partial, coverage: "complete", tracked_tokens: 3 });
  expect(await screen.findByText("3", { exact: true })).toBeVisible();
  expect(screen.queryByRole("note")).not.toBeInTheDocument();
});
