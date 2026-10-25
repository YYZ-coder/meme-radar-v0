import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, expect, it, vi } from "vitest";
import { activitiesSchema, liquidityEventsSchema } from "@/lib/api/schemas";
import type { TokenDetail } from "@/lib/api/types";
import en from "@/messages/en.json";
import zh from "@/messages/zh-CN.json";
import { TokenTabs } from "./token-tabs";

const address = "0x" + "11".repeat(20);
const hash = "0x" + "aa".repeat(32);
const event = {
  id: "110:0:0",
  time: "2026-10-21T12:00:00Z",
  kind: "buy",
  token_amount: "2",
  quote_amount: null,
  quote_symbol: null,
  value_usd: null,
  price_usd: null,
  market_cap_usd: null,
  wallet: { address: "0x" + "22".repeat(20), labels: [], display_name: "Published buyer" },
  attribution: "confirmed",
  tx_hash: hash,
  pool: "0x" + "bb".repeat(20),
  dex: "PancakeSwap",
};
const activity = {
  items: [event],
  next_cursor: null,
  coverage: "partial",
  label_counts_coverage: "partial",
  label_counts: { all: null, smart_money: null },
};
const liquidity = { items: [{ ...event, kind: "add" }], next_cursor: null, coverage: "partial" };
const detail: TokenDetail = {
  token: { chain: "bsc", address, symbol: "REAL", name: "Real token" },
  currency: "USD",
  as_of: event.time,
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
  holder_coverage: { status: "unknown", since: null, as_of: event.time },
  windows: [],
  same_name: [],
};

it("keeps a proved trade readable when quote quantity and metadata are unknown", () => {
  expect(activitiesSchema.parse(activity).items[0]).toMatchObject(event);
});

it("keeps a proved LP event readable when quote quantity and metadata are unknown", () => {
  expect(liquidityEventsSchema.parse(liquidity).items[0]).toMatchObject({
    id: event.id,
    token_amount: "2",
    quote_amount: null,
    quote_symbol: null,
    value_usd: null,
    tx_hash: hash,
    pool: event.pool,
    kind: "add",
  });
});

it.each(["quote_amount", "quote_symbol"])("keeps unknown %s required on event responses", (key) => {
  const missing = Object.fromEntries(Object.entries(event).filter(([field]) => field !== key));
  expect(activitiesSchema.safeParse({ ...activity, items: [missing] }).success).toBe(false);
  expect(
    liquidityEventsSchema.safeParse({ ...liquidity, items: [{ ...missing, kind: "add" }] }).success
  ).toBe(false);
});

const clients: QueryClient[] = [];
afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
  vi.unstubAllGlobals();
});

it.each([
  ["en", "Liquidity", "Liquidity changes", "Smart money"],
  ["zh-CN", "流动性", "流动性变化", "聪明钱"],
] as const)(
  "preserves known event amounts and filters with unknown quotes in %s",
  async (locale, tab, events, smartLabel) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), "http://localhost");
        let body: unknown = { items: [] };
        if (url.pathname.endsWith("/activities")) {
          body =
            url.searchParams.get("label") === "smart_money"
              ? {
                  ...activity,
                  items: [
                    {
                      ...event,
                      wallet: {
                        ...event.wallet,
                        labels: ["smart_money"],
                        display_name: "Confirmed smart wallet",
                      },
                    },
                  ],
                }
              : activity;
        } else if (url.pathname.endsWith("/liquidity-events")) body = liquidity;
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
    const user = userEvent.setup();
    let row = (await screen.findByText("Published buyer")).closest("tr")!;
    const amount = within(row).getAllByRole("cell")[3];
    expect(amount).toHaveTextContent("2-- --");
    expect(within(row).getAllByRole("cell")[4]).toHaveTextContent("--");
    expect(row.querySelector(".tx-link")).toHaveAttribute("href", `https://bscscan.com/tx/${hash}`);
    await user.click(screen.getByRole("button", { name: new RegExp(`^${smartLabel}\\s*--$`) }));
    expect(await screen.findByText("Confirmed smart wallet")).toBeVisible();
    await user.click(screen.getByRole("tab", { name: tab }));
    await user.click(screen.getByRole("button", { name: events }));
    row = (await screen.findByText("Published buyer")).closest("tr")!;
    expect(within(row).getAllByRole("cell")[1]).toHaveTextContent("2-- --");
    expect(within(row).getAllByRole("cell")[2]).toHaveTextContent("--");
    expect(row.querySelector(".tx-link")).toHaveAttribute("href", `https://bscscan.com/tx/${hash}`);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  }
);
