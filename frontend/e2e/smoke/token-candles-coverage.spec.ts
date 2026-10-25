import { test, expect } from "@playwright/test";

const token = "/token/solana/GX1JGwFmrzgkv6MBjwZZdZt3qBcWi1d5ZuAUS3KFpump";
const partial = {
  interval: "1m",
  metric: "price",
  currency: "USD",
  as_of: "2026-10-21T12:04:30Z",
  coverage: "partial",
  empty_reason: null,
  items: [],
  observed_items: [
    { t: 1792584000, o: "2", h: "4", l: "2", c: "3", v: null, closed: true },
    { t: 1792584240, o: "5", h: "6", l: "4", c: "5.5", v: "7", closed: false },
  ],
  gaps: [
    { from: 1792584060, to: 1792584120, reason: "price_unavailable" },
    { from: 1792584120, to: 1792584180, reason: "price_unavailable" },
    { from: 1792584180, to: 1792584240, reason: "price_unavailable" },
  ],
  pool_switches: [],
};

// Controlled HTTP regression; the actual production chart SDK renders the canvas.
// This does not claim the controlled records came from the real backend.
for (const locale of ["zh-CN", "en"] as const) {
  test(`observed candle coverage renders with the real chart SDK in ${locale}`, async ({
    page,
  }, testInfo) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    if (locale === "en") await page.setViewportSize({ width: 390, height: 844 });
    await page.route("**/api/v1/tokens/**/candles?**", (route) => route.fulfill({ json: partial }));
    await page.goto(token);
    if (locale === "en")
      await page.getByRole("button", { name: "切换为 English", exact: true }).click();
    const note = page.locator(".chart-coverage");
    await expect(note).toContainText(
      locale === "en"
        ? "Parsed main-pool candles · incomplete coverage"
        : "已解析主池 K 线 · 覆盖不完整"
    );
    await expect(note).toContainText(
      locale === "en" ? "Current candle is not closed" : "当前 K 线尚未收盘"
    );
    await expect(note).toContainText("2026-10-21 12:04:30 UTC");
    await expect(note).toContainText(
      locale === "en" ? "Unknown prices remain gaps" : "未知价格保留空档"
    );
    for (const indicator of ["MA", "EMA", "BOLL", "MACD", "RSI"]) {
      await expect(page.getByRole("button", { name: indicator, exact: true })).toBeDisabled();
    }
    await expect(page.getByRole("button", { name: "VOL", exact: true })).toBeEnabled();
    await expect(page.getByTestId("token-chart").locator("canvas").first()).toBeVisible();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    ).toBe(true);
    await testInfo.attach(`partial-candles-${locale}`, {
      body: await page.locator(".chart-panel").screenshot(),
      contentType: "image/png",
    });
    expect(errors).toEqual([]);
  });
}

test("candle empty reasons remain distinct in production rendering", async ({ page }) => {
  let reason = "no_main_pool";
  await page.route("**/api/v1/tokens/**/candles?**", (route) =>
    route.fulfill({
      json: { ...partial, empty_reason: reason, observed_items: [], gaps: [] },
    })
  );
  for (const [nextReason, explanation] of [
    ["no_main_pool", "主池尚未确定；满足流动性选择条件后，才能生成主池 K 线。"],
    ["no_price", "所选计价的价格暂不可用；未知价格区间保留空档。"],
    ["no_trades", "这个周期暂无成交"],
  ]) {
    reason = nextReason;
    await page.goto(token);
    await expect(page.getByTestId("token-chart")).toContainText(explanation);
    await expect(page.getByTestId("token-chart").locator("canvas")).toHaveCount(0);
  }
});
