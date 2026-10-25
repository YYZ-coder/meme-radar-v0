import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, expect, it, vi } from "vitest";
import { activitiesSchema, poolsSchema, tokenDetailSchema } from "@/lib/api/schemas";
import type { TokenDetail } from "@/lib/api/types";
import en from "@/messages/en.json";
import zh from "@/messages/zh-CN.json";
import { TokenTabs } from "./token-tabs";

const address = "0x" + "11".repeat(20);
const poolAddress = "0x" + "c1".repeat(20);
const unknownPool = {
  address: poolAddress,
  dex: "Proven pool",
  protocol: "pancakeswap",
  quote_symbol: null,
  liquidity_usd: null,
  reserve_token: "123",
  reserve_quote: null,
  created_at: null,
  status: "active",
  is_main: true,
};
const detail: TokenDetail = {
  token: { chain: "bsc", address, symbol: "REAL", name: "Real token" },
  currency: "USD",
  as_of: "2026-10-21T12:00:00Z",
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
  holder_coverage: { status: "unknown", since: null, as_of: "2026-10-21T12:00:00Z" },
  windows: [],
  same_name: [],
};

it("accepts a proved pool with unknown quote metadata without discarding known token reserves", () => {
  expect(poolsSchema.parse({ items: [unknownPool] }).items[0]).toMatchObject(unknownPool);
});

it("accepts an identified main pool whose quote symbol is unknown", () => {
  const main = { address: poolAddress, dex: "Proven pool", quote_symbol: null };
  expect(tokenDetailSchema.parse({ ...detail, main_pool: main }).main_pool).toEqual(main);
});

it("keeps the quote field required even when its value is unknown", () => {
  const { quote_symbol: omitted, ...missingQuote } = unknownPool;
  expect(omitted).toBeNull();
  expect(poolsSchema.safeParse({ items: [missingQuote] }).success).toBe(false);
});

it("keeps activity quote symbols required even when nullable", () => {
  expect(
    activitiesSchema.safeParse({
      items: [
        {
          id: "110:0:0",
          time: detail.as_of,
          kind: "buy",
          wallet: null,
          attribution: null,
          token_amount: "123",
          quote_amount: "1",
          quote_symbol: undefined,
          value_usd: null,
          price_usd: null,
          market_cap_usd: null,
          tx_hash: "0x" + "aa".repeat(32),
          pool: poolAddress,
          dex: "Proven pool",
        },
      ],
      next_cursor: null,
      coverage: "partial",
      label_counts_coverage: "partial",
      label_counts: { all: null, smart_money: null },
    }).success
  ).toBe(false);
});

const clients: QueryClient[] = [];
afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
  vi.unstubAllGlobals();
});

it.each([
  ["en", "Liquidity"],
  ["zh-CN", "流动性"],
] as const)(
  "retains known pool identity and token reserves with an unknown quote in %s",
  async (locale, liquidity) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), "http://localhost");
        const body = url.pathname.endsWith("/pools")
          ? {
              items: [
                unknownPool,
                {
                  ...unknownPool,
                  address: "0x" + "c2".repeat(20),
                  dex: "Known pool",
                  quote_symbol: "USDT",
                  reserve_token: "456",
                  reserve_quote: "0",
                  is_main: false,
                },
              ],
            }
          : {
              items: [],
              next_cursor: null,
              coverage: "partial",
              label_counts_coverage: "partial",
              label_counts: { all: null, smart_money: null },
            };
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
    await userEvent.setup().click(screen.getByRole("tab", { name: liquidity }));
    const row = (await screen.findByText("Proven pool")).closest("tr")!;
    const cells = within(row).getAllByRole("cell");
    expect(cells[0]).toHaveTextContent("Proven pool · --");
    expect(cells[2]).toHaveTextContent("123");
    expect(within(cells[2]).getByText("-- --")).toBeVisible();
    expect(row.querySelector(".address")).toHaveAttribute("title", poolAddress);
    const known = screen.getByText("Known pool").closest("tr")!;
    expect(known).toHaveTextContent("USDT");
    expect(within(known).getAllByRole("cell")[2]).toHaveTextContent("4560 USDT");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  }
);
