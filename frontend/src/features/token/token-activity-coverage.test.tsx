import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, expect, it, vi } from "vitest";
import type { ActivitiesResponse, TokenDetail } from "@/lib/api/types";
import en from "@/messages/en.json";
import zh from "@/messages/zh-CN.json";
import { TokenTabs } from "./token-tabs";

const time = "2026-10-21T12:00:00Z";
const address = "0x" + "11".repeat(20);
const event = {
  id: "published-1",
  time,
  kind: "buy" as const,
  wallet: { address: "0x" + "22".repeat(20), labels: [], display_name: "Published buyer" },
  attribution: "confirmed" as const,
  token_amount: "17",
  quote_amount: "3",
  quote_symbol: "ETH",
  value_usd: null,
  price_usd: null,
  market_cap_usd: null,
  tx_hash: "0x" + "aa".repeat(32),
  pool: "0x" + "bb".repeat(32),
  dex: "uniswap_v4",
};
const partial: ActivitiesResponse = {
  items: [event],
  next_cursor: null,
  coverage: "partial",
  label_counts_coverage: "partial",
  label_counts: { all: null, smart_money: null },
};
const detail: TokenDetail = {
  token: { chain: "base", address, symbol: "REAL", name: "Real token" },
  currency: "USD",
  as_of: time,
  decimals: 18,
  total_supply: null,
  socials: {},
  creator: null,
  main_pool: null,
  price: null,
  market_cap: null,
  liquidity: null,
  volume_24h: null,
  price_change_24h_pct: null,
  holders: null,
  sm_holders: null,
  ath_market_cap: null,
  ath_since: null,
  holder_coverage: { status: "unknown", since: null, as_of: time },
  windows: [],
  same_name: [],
};
const clients: QueryClient[] = [];
afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
  vi.unstubAllGlobals();
});

function show(data = partial, locale: "en" | "zh-CN" = "en", emptyLiquidity = false) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      let body: unknown;
      if (url.pathname.endsWith("/activities")) {
        body =
          url.searchParams.get("label") === "smart_money"
            ? {
                ...data,
                items: data.items.map((item) => ({
                  ...item,
                  wallet: {
                    ...event.wallet,
                    display_name: "Confirmed smart wallet",
                    labels: ["smart_money"],
                  },
                })),
              }
            : data;
      } else if (url.pathname.endsWith("/pools")) {
        body = { items: [] };
      } else if (url.pathname.endsWith("/liquidity-events")) {
        body = {
          coverage: "partial",
          items: emptyLiquidity ? [] : [{ ...event, kind: "add" }],
          next_cursor: null,
        };
      } else {
        throw new Error("unexpected public token request");
      }
      return new Response(JSON.stringify(body), {
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
        <TokenTabs detail={detail} />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
  return userEvent.setup();
}

it.each([
  [
    "en",
    "Activity · incomplete coverage",
    "24h label counts · incomplete coverage",
    "Trader",
    "All",
    "Smart money",
  ],
  ["zh-CN", "活动·覆盖不完整", "24h 标签计数·覆盖不完整", "交易者", "全部", "聪明钱"],
] as const)(
  "keeps published events and unknown counts selectable in %s",
  async (locale, feed, labels, trader, all, smart) => {
    const user = show(partial, locale);
    expect(await screen.findByText("Published buyer")).toBeVisible();
    expect(screen.getByText(feed)).toBeVisible();
    expect(screen.getByText(labels)).toBeVisible();
    const chips = within(screen.getByRole("group", { name: trader }));
    expect(chips.getByRole("button", { name: new RegExp(`^${all}\\s*--$`) })).toBeEnabled();
    const sm = chips.getByRole("button", { name: new RegExp(`^${smart}\\s*--$`) });
    expect(sm).toBeEnabled();
    const row = screen.getByText("Published buyer").closest("tr");
    expect(within(row!).getAllByText("--").length).toBeGreaterThan(0);
    await user.click(sm);
    expect(await screen.findByText("Confirmed smart wallet")).toBeVisible();
    expect(chips.getByRole("button", { name: new RegExp(`^${smart}\\s*--$`) })).toHaveAttribute(
      "aria-pressed",
      "true"
    );
  }
);

it("keeps a complete short feed separate from incomplete 24h counts", async () => {
  show({ ...partial, coverage: "complete" });
  expect(await screen.findByText("Published buyer")).toBeVisible();
  expect(screen.getByText("24h label counts · incomplete coverage")).toBeVisible();
  expect(screen.queryByText("Activity · incomplete coverage")).not.toBeInTheDocument();
});

it("retains complete zero label counts when the broader feed is partial", async () => {
  show({ ...partial, label_counts_coverage: "complete", label_counts: { all: 3, smart_money: 0 } });
  expect(await screen.findByText("Published buyer")).toBeVisible();
  expect(screen.getByText("Activity · incomplete coverage")).toBeVisible();
  expect(screen.queryByText("24h label counts · incomplete coverage")).not.toBeInTheDocument();
  const chips = within(screen.getByRole("group", { name: "Trader" }));
  expect(chips.getByRole("button", { name: /^All\s*3$/ })).toBeEnabled();
  expect(chips.getByRole("button", { name: /^Smart money\s*0$/ })).toBeEnabled();
});

it("keeps an empty partial feed distinct from an unavailable API", async () => {
  show({ ...partial, items: [] });
  expect(await screen.findByRole("heading", { name: "Activity" })).toBeVisible();
  expect(screen.getByText("Activity · incomplete coverage")).toBeVisible();
  expect(screen.getByText("24h label counts · incomplete coverage")).toBeVisible();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("keeps complete events and counts unchanged", async () => {
  show({
    ...partial,
    coverage: "complete",
    label_counts_coverage: "complete",
    label_counts: { all: 3, smart_money: 1 },
  });
  expect(await screen.findByText("Published buyer")).toBeVisible();
  expect(screen.queryByRole("note")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: /^Smart money\s*1$/ })).toBeEnabled();
});

it.each([false, true])("labels partial liquidity event coverage, empty=%s", async (empty) => {
  const user = show(partial, "en", empty);
  await user.click(screen.getByRole("tab", { name: "Liquidity" }));
  await user.click(screen.getByRole("button", { name: "Liquidity changes" }));
  expect(await screen.findByText("Liquidity events · incomplete coverage")).toBeVisible();
  if (empty) expect(screen.getByRole("heading", { name: "Liquidity changes" })).toBeVisible();
  else expect(screen.getByText("Published buyer")).toBeVisible();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});
