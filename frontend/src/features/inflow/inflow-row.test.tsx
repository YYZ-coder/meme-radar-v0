import { render, screen, within } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { describe, expect, it, vi } from "vitest";
import type { InflowRow } from "@/lib/api/types";
import en from "@/messages/en.json";
import zh from "@/messages/zh-CN.json";
import { InflowTableRow } from "./inflow-row";

const partial: InflowRow & {
  observed: { trades: number; buys: number; sells: number };
  coverage: "partial" | "complete";
} = {
  token: { chain: "base", address: "0x" + "11".repeat(20), symbol: "OBS", name: "Observed" },
  currency: "USD",
  price: null,
  market_cap: null,
  price_change_pct: null,
  liquidity: null,
  trades: null,
  buys: null,
  sells: null,
  observed: { trades: 7, buys: 4, sells: 3 },
  coverage: "partial",
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

function show(row = partial, locale: "en" | "zh-CN" = "en") {
  render(
    <NextIntlClientProvider locale={locale} messages={locale === "en" ? en : zh}>
      <table>
        <tbody>
          <InflowTableRow
            row={row}
            locale={locale}
            starred={false}
            hot={false}
            stale={false}
            index={0}
            onStar={vi.fn()}
            onOpen={vi.fn()}
          />
        </tbody>
      </table>
    </NextIntlClientProvider>
  );
  return within(screen.getAllByRole("cell")[6]);
}

describe("independent parsed trade observations", () => {
  it.each([
    ["en", "Parsed · incomplete coverage", "Parsed 7 · Buy 4 / Sell 3"],
    ["zh-CN", "已解析·覆盖不完整", "已解析 7 · 买入 4 / 卖出 3"],
  ] as const)(
    "labels partial counts in %s while complete totals and USD stay unknown",
    (locale, label, counts) => {
      const cell = show(partial, locale);
      expect(cell.getByText(label)).toBeVisible();
      expect(cell.getByText(counts)).toBeVisible();
      expect(cell.getAllByText("--").length).toBeGreaterThan(0);
      expect(screen.getAllByRole("cell")[7]).toHaveTextContent("--");
    }
  );

  it("shows a parsed zero only as an incomplete observation", () => {
    const cell = show({ ...partial, observed: { trades: 0, buys: 0, sells: 0 } });
    expect(cell.getByText("Parsed · incomplete coverage")).toBeVisible();
    expect(cell.getByText("Parsed 0 · Buy 0 / Sell 0")).toBeVisible();
    expect(cell.getAllByText("--").length).toBeGreaterThan(0);
  });

  it("keeps complete counts as the sole normal trade display", () => {
    const cell = show({ ...partial, trades: 7, buys: 4, sells: 3, coverage: "complete" });
    expect(cell.getByText("7")).toBeVisible();
    expect(cell.queryByText("Parsed · incomplete coverage")).not.toBeInTheDocument();
  });
});
