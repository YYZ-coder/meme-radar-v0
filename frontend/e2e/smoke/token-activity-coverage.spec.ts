import { expect, test } from "@playwright/test";

const tokenPath = "/token/base/0x7d4bfd446d4b81d5ede62529f9c8db6fdd81e222";
const activity = {
  id: "published-token-activity",
  time: "2026-10-21T12:00:00Z",
  kind: "buy",
  wallet: { address: "0x" + "22".repeat(20), labels: [], display_name: "Published buyer" },
  attribution: "confirmed",
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

for (const locale of ["zh-CN", "en"] as const) {
  test(`token activity partial coverage stays usable in ${locale}`, async ({ page }) => {
    const mobile = locale === "en";
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    let mode: "partial" | "empty" | "short" | "complete" | "unknown" = "partial";
    await page.route(/\/api\/v1\/tokens\/base\/[^/]+\/activities(?:\?|$)/, async (route) => {
      const params = new URL(route.request().url()).searchParams;
      const smart = params.get("label") === "smart_money";
      const observed =
        mode === "unknown"
          ? { ...activity, token_amount: "2", quote_amount: null, quote_symbol: null }
          : activity;
      const item = smart
        ? {
            ...observed,
            wallet: {
              ...activity.wallet,
              labels: ["smart_money"],
              display_name: "Confirmed smart buyer",
            },
          }
        : observed;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          coverage: mode === "complete" || mode === "short" ? "complete" : "partial",
          label_counts_coverage: mode === "complete" ? "complete" : "partial",
          label_counts:
            mode === "complete" ? { all: 3, smart_money: 1 } : { all: null, smart_money: null },
          // Chart-marker requests share this schema. An empty partial marker
          // subset is valid and must not make its existing chart fail.
          items: mode === "empty" || params.has("from") ? [] : [item],
          next_cursor: null,
        }),
      });
    });
    await page.route(/\/api\/v1\/tokens\/base\/[^/]+\/liquidity-events(?:\?|$)/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          coverage: "partial",
          items: [
            {
              ...activity,
              kind: "add",
              ...(mode === "unknown"
                ? { token_amount: "2", quote_amount: null, quote_symbol: null }
                : {}),
            },
          ],
          next_cursor: null,
        }),
      });
    });
    await page.goto(tokenPath);
    if (locale === "en")
      await page.getByRole("button", { name: "切换为 English", exact: true }).click();
    const text =
      locale === "en"
        ? {
            feed: "Activity · incomplete coverage",
            counts: "24h label counts · incomplete coverage",
            trader: "Trader",
            all: "All",
            smart: "Smart money",
            activity: "Activity",
            liquidity: "Liquidity",
            events: "Liquidity changes",
            eventCoverage: "Liquidity events · incomplete coverage",
          }
        : {
            feed: "活动·覆盖不完整",
            counts: "24h 标签计数·覆盖不完整",
            trader: "交易者",
            all: "全部",
            smart: "聪明钱",
            activity: "活动",
            liquidity: "流动性",
            events: "流动性变化",
            eventCoverage: "加减池活动·覆盖不完整",
          };
    const panel = page.locator(".token-tabs");
    await expect(panel.getByText(text.feed, { exact: true })).toBeVisible();
    await expect(panel.getByText(text.counts, { exact: true })).toBeVisible();
    const chips = panel.getByRole("group", { name: text.trader, exact: true });
    const smart = chips.getByRole("button", { name: new RegExp(`^${text.smart}`) });
    await expect(smart).toBeEnabled();
    await expect(smart).toContainText("--");
    const row = panel.locator(".data-table tbody tr").first();
    await expect(row).toContainText("Published buyer");
    await expect(row.getByRole("cell").nth(2)).toHaveText("--");
    await expect(row.getByRole("cell").nth(4)).toHaveText("--");
    await smart.click();
    await expect(row).toContainText("Confirmed smart buyer");
    await expect(smart).toHaveAttribute("aria-pressed", "true");
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    ).toBe(true);
    await page.screenshot({
      path: test.info().outputPath(`token-activities-partial-${locale}.png`),
      fullPage: true,
    });

    await panel.getByRole("tab", { name: text.liquidity, exact: true }).click();
    await panel.getByRole("button", { name: text.events, exact: true }).click();
    await expect(panel.getByText(text.eventCoverage, { exact: true })).toBeVisible();
    await expect(panel.locator(".data-table tbody tr").first()).toContainText("Published buyer");

    mode = "empty";
    await page.reload();
    await expect(panel.getByRole("heading", { name: text.activity, exact: true })).toBeVisible();
    await expect(panel.getByText(text.feed, { exact: true })).toBeVisible();
    await expect(panel.getByText(text.counts, { exact: true })).toBeVisible();
    await expect(panel.locator(".data-table tbody tr")).toHaveCount(0);

    mode = "short";
    await page.reload();
    await expect(panel.locator(".data-table tbody tr").first()).toContainText("Published buyer");
    await expect(panel.getByText(text.feed, { exact: true })).not.toBeVisible();
    await expect(panel.getByText(text.counts, { exact: true })).toBeVisible();

    mode = "complete";
    await page.reload();
    await expect(panel.locator(".data-table tbody tr").first()).toContainText("Published buyer");
    await expect(panel.getByText(text.feed, { exact: true })).not.toBeVisible();
    await expect(panel.getByText(text.counts, { exact: true })).not.toBeVisible();
    await expect(chips.getByRole("button", { name: new RegExp(`^${text.all}`) })).toContainText(
      "3"
    );
    await expect(smart).toContainText("1");

    mode = "unknown";
    await page.reload();
    await expect(row).toContainText("Published buyer");
    await expect(row.getByRole("cell").nth(3)).toHaveText("2-- --");
    await expect(row.getByRole("cell").nth(4)).toHaveText("--");
    await smart.click();
    await expect(row).toContainText("Confirmed smart buyer");
    await expect(row.getByRole("cell").nth(3)).toHaveText("2-- --");
    await panel.getByRole("tab", { name: text.liquidity, exact: true }).click();
    await panel.getByRole("button", { name: text.events, exact: true }).click();
    await expect(row).toContainText("Published buyer");
    await expect(row.getByRole("cell").nth(1)).toHaveText("2-- --");
    await expect(row.getByRole("cell").nth(2)).toHaveText("--");
    await page.screenshot({
      path: test.info().outputPath(`token-events-unknown-quote-${locale}.png`),
      fullPage: true,
    });
    expect(errors).toEqual([]);
  });
}
