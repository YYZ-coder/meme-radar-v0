import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, expect, it, vi } from "vitest";
import en from "@/messages/en.json";
import zh from "@/messages/zh-CN.json";
import { TokenChart } from "./token-chart";

// The external display SDK needs a canvas engine. Observe its rendered data at
// that system boundary while the public chart uses the real API client/schema.
const display = vi.hoisted(() => ({
  series: [] as { type: string; data: unknown[] }[],
  markers: [] as unknown[],
  visibleRange: null as { from: number; to: number } | null,
}));
vi.mock("lightweight-charts", () => ({
  ColorType: { Solid: "solid" },
  CrosshairMode: { Normal: 0 },
  PriceScaleMode: { Normal: 0, Logarithmic: 1 },
  CandlestickSeries: "candlestick",
  HistogramSeries: "histogram",
  LineSeries: "line",
  createChart(host: HTMLElement) {
    display.series = [];
    display.markers = [];
    display.visibleRange = null;
    const canvas = document.createElement("canvas");
    host.append(canvas);
    return {
      addSeries(type: string) {
        const series = { type, data: [] as unknown[] };
        display.series.push(series);
        return {
          setData: (data: unknown[]) => {
            series.data = data;
          },
        };
      },
      panes: () => [],
      timeScale: () => ({
        fitContent() {
          display.visibleRange = {
            from: 0,
            to:
              (display.series.find((series) => series.type === "candlestick")?.data.length ?? 1) -
              1,
          };
        },
        setVisibleLogicalRange(range: { from: number; to: number }) {
          display.visibleRange = range;
        },
      }),
      remove: () => canvas.remove(),
    };
  },
  createSeriesMarkers(_series: unknown, markers: unknown[]) {
    display.markers = markers;
  },
}));

const clients: QueryClient[] = [];
const noMainPool = {
  interval: "1m",
  metric: "price",
  currency: "USD",
  as_of: "2026-10-21T12:01:30Z",
  coverage: "partial",
  empty_reason: "no_main_pool",
  items: [],
  observed_items: [],
  gaps: [],
  pool_switches: [],
};

afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
  vi.unstubAllGlobals();
  display.series = [];
  display.markers = [];
  display.visibleRange = null;
});

it.each([
  [
    "en",
    "Parsed main-pool candles · incomplete coverage",
    "Current candle is not closed",
    "Indicators are unavailable while candle coverage is incomplete.",
  ],
  [
    "zh-CN",
    "已解析主池 K 线 · 覆盖不完整",
    "当前 K 线尚未收盘",
    "K 线覆盖不完整，技术指标暂不可用。",
  ],
] as const)(
  "shows observed prices, gaps and unknown volume honestly in %s",
  async (locale, partial, open, indicatorNote) => {
    const response = {
      ...noMainPool,
      as_of: "2026-10-21T12:02:30Z",
      empty_reason: null,
      observed_items: [
        { t: 1792584000, o: "2", h: "4", l: "2", c: "3", v: null, closed: true },
        { t: 1792584120, o: "5", h: "6", l: "4", c: "5.5", v: "6", closed: false },
      ],
      gaps: [{ from: 1792584060, to: 1792584120, reason: "price_unavailable" }],
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), "http://localhost");
        return new Response(
          JSON.stringify(
            url.pathname.endsWith("/candles")
              ? response
              : {
                  items: [],
                  next_cursor: null,
                  coverage: "partial",
                  label_counts_coverage: "partial",
                  label_counts: { all: null, smart_money: null },
                }
          ),
          { status: 200 }
        );
      })
    );
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    clients.push(client);
    render(
      <QueryClientProvider client={client}>
        <NextIntlClientProvider locale={locale} messages={locale === "en" ? en : zh}>
          <TokenChart
            chain="base"
            address={"0x" + "11".repeat(20)}
            currency="usd"
            nativeSymbol="ETH"
          />
        </NextIntlClientProvider>
      </QueryClientProvider>
    );

    expect(await screen.findByText(partial)).toBeVisible();
    expect(screen.getByText(open)).toBeVisible();
    expect(screen.getByText(indicatorNote)).toBeVisible();
    expect(screen.getByText(/2026-10-21 12:02:30 UTC/)).toBeVisible();
    for (const indicator of ["MA", "EMA", "BOLL", "MACD", "RSI"]) {
      expect(screen.getByRole("button", { name: indicator })).toBeDisabled();
    }
    await waitFor(() =>
      expect(
        display.series.find((series) => series.type === "candlestick")?.data.slice(-3)
      ).toEqual([
        { time: 1792584000, open: 2, high: 4, low: 2, close: 3 },
        { time: 1792584060 },
        { time: 1792584120, open: 5, high: 6, low: 4, close: 5.5 },
      ])
    );
    expect(display.series.some((series) => series.type === "line")).toBe(false);
    expect(display.series.find((series) => series.type === "histogram")?.data.slice(-3)).toEqual([
      { time: 1792584000 },
      { time: 1792584060 },
      { time: 1792584120, value: 6, color: "#a5df7866" },
    ]);
  }
);

it.each([
  [
    "en",
    "A main pool has not been selected yet. Candles require the liquidity selection criteria to be met.",
    "No trades in this interval",
  ],
  ["zh-CN", "主池尚未确定；满足流动性选择条件后，才能生成主池 K 线。", "这个周期暂无成交"],
] as const)(
  "explains the missing main pool instead of claiming no trades in %s",
  async (locale, explanation, empty) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify(noMainPool), { status: 200 }))
    );
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    clients.push(client);
    render(
      <QueryClientProvider client={client}>
        <NextIntlClientProvider locale={locale} messages={locale === "en" ? en : zh}>
          <TokenChart
            chain="base"
            address={"0x" + "11".repeat(20)}
            currency="usd"
            nativeSymbol="ETH"
          />
        </NextIntlClientProvider>
      </QueryClientProvider>
    );
    expect(await screen.findByText(explanation)).toBeVisible();
    expect(screen.queryByText(empty)).not.toBeInTheDocument();
  }
);

it("places pool and wallet markers only on real bars using the selected 30s interval", async () => {
  const pool = "0x" + "22".repeat(20);
  const response = {
    ...noMainPool,
    interval: "30s",
    as_of: "2026-10-21T12:03:00Z",
    empty_reason: null,
    observed_items: [
      { t: 1792584030, o: "2", h: "4", l: "2", c: "3", v: null, closed: true },
      { t: 1792584120, o: "5", h: "6", l: "4", c: "5.5", v: null, closed: true },
    ],
    pool_switches: [
      { t: 1792584040, from_pool: null, to_pool: pool, reason: "liquidity" },
      { t: 1792584070, from_pool: null, to_pool: pool, reason: "graduation" },
    ],
  };
  const activities = {
    coverage: "partial",
    label_counts_coverage: "partial",
    next_cursor: null,
    label_counts: { all: null, smart_money: null },
    items: [1792584045, 1792584075, 1792584135].map((time, i) => ({
      id: String(i),
      time: new Date(time * 1000).toISOString(),
      kind: "buy",
      wallet: { address: "0x" + "33".repeat(20), labels: ["smart_money"], display_name: null },
      attribution: "confirmed",
      token_amount: "1",
      quote_amount: null,
      quote_symbol: null,
      value_usd: null,
      price_usd: null,
      market_cap_usd: null,
      tx_hash: "0x" + "44".repeat(32),
      pool,
      dex: "uniswap_v2",
    })),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async (input: RequestInfo | URL) =>
        new Response(JSON.stringify(String(input).includes("/candles") ? response : activities), {
          status: 200,
        })
    )
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en" messages={en}>
        <TokenChart
          chain="base"
          address={"0x" + "11".repeat(20)}
          currency="usd"
          nativeSymbol="ETH"
        />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
  fireEvent.click(screen.getByRole("button", { name: "30s" }));
  await waitFor(() =>
    expect(display.markers).toEqual([
      { time: 1792584030, position: "belowBar", color: "#a5df78", shape: "arrowUp", text: "B" },
      {
        time: 1792584030,
        position: "aboveBar",
        color: "#b5f268",
        shape: "square",
        text: "Main pool switch",
      },
      { time: 1792584120, position: "belowBar", color: "#a5df78", shape: "arrowUp", text: "B" },
    ])
  );
});

it.each([
  [
    "en",
    "no_price",
    "Price is unavailable for the selected currency; unknown buckets remain gaps.",
  ],
  ["zh-CN", "no_price", "所选计价的价格暂不可用；未知价格区间保留空档。"],
  ["en", null, "This range has incomplete coverage and no priced candles to display."],
  ["zh-CN", null, "该范围覆盖不完整，暂无可展示价格的 K 线。"],
] as const)(
  "distinguishes missing prices and incomplete empty coverage in %s (%s)",
  async (locale, reason, explanation) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ ...noMainPool, empty_reason: reason }), { status: 200 })
      )
    );
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    clients.push(client);
    render(
      <QueryClientProvider client={client}>
        <NextIntlClientProvider locale={locale} messages={locale === "en" ? en : zh}>
          <TokenChart
            chain="base"
            address={"0x" + "11".repeat(20)}
            currency="usd"
            nativeSymbol="ETH"
          />
        </NextIntlClientProvider>
      </QueryClientProvider>
    );
    expect(await screen.findByText(explanation)).toBeVisible();
    expect(
      screen.queryByText(locale === "en" ? "No trades in this interval" : "这个周期暂无成交")
    ).not.toBeInTheDocument();
  }
);

it("keeps every missing minute as whitespace and disables indicators on sparse complete coverage", async () => {
  const response = {
    ...noMainPool,
    as_of: "2026-10-21T12:04:30Z",
    coverage: "complete",
    empty_reason: null,
    items: [
      { t: 1792584000, o: "2", h: "4", l: "2", c: "3", v: "7" },
      { t: 1792584240, o: "5", h: "6", l: "4", c: "5.5", v: "6" },
    ],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async (input: RequestInfo | URL) =>
        new Response(
          JSON.stringify(
            String(input).includes("/candles")
              ? response
              : {
                  items: [],
                  next_cursor: null,
                  coverage: "partial",
                  label_counts_coverage: "partial",
                  label_counts: { all: null, smart_money: null },
                }
          ),
          { status: 200 }
        )
    )
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en" messages={en}>
        <TokenChart
          chain="base"
          address={"0x" + "11".repeat(20)}
          currency="usd"
          nativeSymbol="ETH"
        />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
  await waitFor(() =>
    expect(display.series.find((series) => series.type === "candlestick")?.data).toHaveLength(300)
  );
  const prices = display.series.find((series) => series.type === "candlestick")!.data;
  expect(prices[0]).toEqual({ time: 1792566300 });
  expect(prices.slice(-5)).toEqual([
    { time: 1792584000, open: 2, high: 4, low: 2, close: 3 },
    { time: 1792584060 },
    { time: 1792584120 },
    { time: 1792584180 },
    { time: 1792584240, open: 5, high: 6, low: 4, close: 5.5 },
  ]);
  expect(screen.getByText("Complete closed candles")).toBeVisible();
  expect(screen.getByText("Indicators require continuous priced candles.")).toBeVisible();
  for (const indicator of ["MA", "EMA", "BOLL", "MACD", "RSI"]) {
    expect(screen.getByRole("button", { name: indicator })).toBeDisabled();
  }
  expect(display.series.some((series) => series.type === "line")).toBe(false);
});

// Existing no-trades behavior remains valid only when explicitly reported.
it.each(["en", "zh-CN"] as const)(
  "keeps explicit no-trades as a distinct state in %s",
  async (locale) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({ ...noMainPool, coverage: "complete", empty_reason: "no_trades" }),
            { status: 200 }
          )
      )
    );
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    clients.push(client);
    render(
      <QueryClientProvider client={client}>
        <NextIntlClientProvider locale={locale} messages={locale === "en" ? en : zh}>
          <TokenChart
            chain="base"
            address={"0x" + "11".repeat(20)}
            currency="usd"
            nativeSymbol="ETH"
          />
        </NextIntlClientProvider>
      </QueryClientProvider>
    );
    expect(
      await screen.findByText(locale === "en" ? "No trades in this interval" : "这个周期暂无成交")
    ).toBeVisible();
    expect(
      screen.getByText(locale === "en" ? "Complete closed candles" : "完整已收盘 K 线")
    ).toBeVisible();
  }
);

it("keeps all 300 closed bars before an aligned exclusive as_of without a future slot", async () => {
  const response = {
    ...noMainPool,
    as_of: "2026-10-21T12:00:00Z",
    coverage: "complete",
    empty_reason: null,
    items: Array.from({ length: 300 }, (_, index) => ({
      t: 1792566000 + index * 60,
      o: "2",
      h: "4",
      l: "2",
      c: "3",
      v: "7",
    })),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async (input: RequestInfo | URL) =>
        new Response(
          JSON.stringify(
            String(input).includes("/candles")
              ? response
              : {
                  items: [],
                  next_cursor: null,
                  coverage: "partial",
                  label_counts_coverage: "partial",
                  label_counts: { all: null, smart_money: null },
                }
          ),
          { status: 200 }
        )
    )
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en" messages={en}>
        <TokenChart
          chain="base"
          address={"0x" + "11".repeat(20)}
          currency="usd"
          nativeSymbol="ETH"
        />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
  await waitFor(() =>
    expect(display.series.find((series) => series.type === "candlestick")?.data[0]).toEqual({
      time: 1792566000,
      open: 2,
      high: 4,
      low: 2,
      close: 3,
    })
  );
  const prices = display.series.find((series) => series.type === "candlestick")!.data;
  expect(prices).toHaveLength(300);
  expect(prices.at(-1)).toEqual({ time: 1792583940, open: 2, high: 4, low: 2, close: 3 });
  expect(prices).not.toContainEqual({ time: 1792584000 });
  expect(screen.getByRole("button", { name: "MA" })).toBeEnabled();
});

it.each([
  ["sparse", 270],
  ["full", 0],
] as const)(
  "starts the %s chart near actual bars while keeping the complete 300-slot timeline",
  async (history, from) => {
    const bars =
      history === "sparse"
        ? [
            { t: 1792584000, o: "2", h: "4", l: "2", c: "3", v: null, closed: true },
            { t: 1792584240, o: "5", h: "6", l: "4", c: "5.5", v: "6", closed: false },
          ]
        : Array.from({ length: 300 }, (_, index) => ({
            t: 1792566300 + index * 60,
            o: "2",
            h: "4",
            l: "2",
            c: "3",
            v: "7",
            closed: true,
          }));
    const response = {
      ...noMainPool,
      as_of: "2026-10-21T12:04:30Z",
      empty_reason: null,
      observed_items: bars,
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async (input: RequestInfo | URL) =>
          new Response(
            JSON.stringify(
              String(input).includes("/candles")
                ? response
                : {
                    items: [],
                    next_cursor: null,
                    coverage: "partial",
                    label_counts_coverage: "partial",
                    label_counts: { all: null, smart_money: null },
                  }
            ),
            { status: 200 }
          )
      )
    );
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    clients.push(client);
    render(
      <QueryClientProvider client={client}>
        <NextIntlClientProvider locale="en" messages={en}>
          <TokenChart
            chain="base"
            address={"0x" + "11".repeat(20)}
            currency="usd"
            nativeSymbol="ETH"
          />
        </NextIntlClientProvider>
      </QueryClientProvider>
    );
    await waitFor(() => expect(display.visibleRange).toEqual({ from, to: 299 }));
    const prices = display.series.find((series) => series.type === "candlestick")!.data;
    expect(prices).toHaveLength(300);
    if (history === "sparse") {
      expect(prices.slice(-5)).toEqual([
        { time: 1792584000, open: 2, high: 4, low: 2, close: 3 },
        { time: 1792584060 },
        { time: 1792584120 },
        { time: 1792584180 },
        { time: 1792584240, open: 5, high: 6, low: 4, close: 5.5 },
      ]);
    } else {
      expect(prices[0]).toEqual({ time: 1792566300, open: 2, high: 4, low: 2, close: 3 });
    }
  }
);
