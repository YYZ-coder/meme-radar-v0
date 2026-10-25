import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, expect, it, vi } from "vitest";
import en from "@/messages/en.json";
import zh from "@/messages/zh-CN.json";
import { OverviewCards } from "./overview-cards";

vi.mock("next/navigation", () => ({ useSearchParams: () => new URLSearchParams("window=1m") }));

const complete = {
  as_of: "2026-10-22T12:00:00Z",
  list_version: "1",
  window: "1m",
  coverage: "complete",
  net_inflow_usd: "10000",
  net_inflow_change_pct: null,
  active_wallets: 2,
  tracked_tokens: 1,
  new_tokens: 0,
  buy_volume_usd: "10000",
  sell_volume_usd: "0",
  buy_share_pct: "100",
  large_buy_volume_usd: "6000",
  small_buy_volume_usd: "4000",
  large_buy_share_pct: "60",
  small_buy_share_pct: "40",
  inflow_bars: [],
};
const clients: QueryClient[] = [];
afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
  vi.unstubAllGlobals();
});

it.each([
  ["en", "Buy volume by position", "No buys"],
  ["zh-CN", "买入金额分布", "暂无买入"],
] as const)(
  "distinguishes known zero buys from unknown statistics in %s",
  async (locale, groupName, empty) => {
    show(
      {
        ...complete,
        net_inflow_usd: "0",
        active_wallets: 0,
        buy_volume_usd: "0",
        buy_share_pct: null,
        large_buy_volume_usd: "0",
        small_buy_volume_usd: "0",
        large_buy_share_pct: null,
        small_buy_share_pct: null,
      },
      locale
    );
    const split = screen.getByRole("group", { name: groupName });
    expect(await within(split).findByText(empty, { exact: true })).toBeVisible();
    expect(within(split).getAllByText("$0", { exact: true })).toHaveLength(2);
    expect(within(split).getAllByText("--", { exact: true })).toHaveLength(2);
    expect(within(split).queryByRole("img")).not.toBeInTheDocument();
    expect(within(split).queryByText("0.00%", { exact: true })).not.toBeInTheDocument();
  }
);

it("keeps known buy shares when only sell USD is unknown", async () => {
  show({ ...complete, sell_volume_usd: null, buy_share_pct: null, net_inflow_usd: null });
  const split = screen.getByRole("group", { name: "Buy volume by position" });
  expect(await within(split).findByText("60.00%", { exact: true })).toBeVisible();
  expect(within(split).getByText("40.00%", { exact: true })).toBeVisible();
  expect(within(split).getByRole("img")).toHaveAccessibleName(/60\.00%.*40\.00%/);
});

it("never presents partial-window values as a complete buy-size distribution", async () => {
  show({ ...complete, coverage: "partial" });
  await screen.findByRole("note");
  const split = screen.getByRole("group", { name: "Buy volume by position" });
  expect(await within(split).findByText("Stats pending", { exact: true })).toBeVisible();
  expect(within(split).getAllByText("--", { exact: true })).toHaveLength(4);
  expect(within(split).queryByRole("img")).not.toBeInTheDocument();
});

function show(data: unknown = complete, locale: "en" | "zh-CN" = "en") {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname !== "/api/v1/smart-money/overview") {
        throw new Error("unexpected overview request");
      }
      return new Response(JSON.stringify(data), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    })
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale={locale} messages={locale === "en" ? en : zh}>
        <OverviewCards chain="base" paused />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

it.each([
  ["en", "Buy volume by position", "Large buys", "Small buys", "$6.00K", "$4.00K"],
  ["zh-CN", "买入金额分布", "大仓买入", "小仓买入", "$6,000", "$4,000"],
] as const)(
  "shows the published buy amounts and amount shares in %s",
  async (locale, groupName, largeLabel, smallLabel, largeAmount, smallAmount) => {
    show(complete, locale);
    const split = screen.getByRole("group", { name: groupName });
    expect(await within(split).findByText("60.00%", { exact: true })).toBeVisible();
    const large = within(split).getByText(largeLabel).closest("div");
    const small = within(split).getByText(smallLabel).closest("div");
    expect(within(large!).getByText(largeAmount, { exact: true })).toBeVisible();
    expect(within(small!).getByText("40.00%", { exact: true })).toBeVisible();
    expect(within(small!).getByText(smallAmount, { exact: true })).toBeVisible();
    expect(split).toHaveAttribute("title", expect.stringContaining("5,000"));
    expect(within(split).getByRole("img")).toHaveAccessibleName(/60\.00%.*40\.00%/);
  }
);

it.each([
  ["en", "Buy volume by position", "Stats pending"],
  ["zh-CN", "买入金额分布", "统计待补齐"],
] as const)(
  "keeps incomplete buy-size statistics unknown in %s",
  async (locale, groupName, pending) => {
    show(
      {
        ...complete,
        coverage: "partial",
        net_inflow_usd: null,
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
      },
      locale
    );
    await screen.findByRole("note");
    const split = screen.getByRole("group", { name: groupName });
    expect(await within(split).findByText(pending, { exact: true })).toBeVisible();
    expect(within(split).getAllByText("--", { exact: true })).toHaveLength(4);
    expect(within(split).queryByRole("img")).not.toBeInTheDocument();
    expect(within(split).queryByText(/\d+\.\d+%/)).not.toBeInTheDocument();
  }
);
