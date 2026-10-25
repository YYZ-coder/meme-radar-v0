import { expect, test } from "@playwright/test";

const token = "0x7d4bfd446d4b81d5ede62529f9c8db6fdd81e222";
const pool = "0x" + "c1".repeat(20);

for (const locale of ["zh-CN", "en"] as const) {
  test(`proved pool with unknown quote remains readable in ${locale}`, async ({ page }) => {
    if (locale === "en") await page.setViewportSize({ width: 390, height: 844 });
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.route(new RegExp(`/api/v1/tokens/base/${token}(?:\\?|$)`), async (route) => {
      const response = await route.fetch();
      const detail = await response.json();
      await route.fulfill({
        response,
        json: {
          ...detail,
          main_pool: { address: pool, dex: "Proven pool", quote_symbol: null },
        },
      });
    });
    await page.route(new RegExp(`/api/v1/tokens/base/${token}/pools(?:\\?|$)`), async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          items: [
            {
              address: pool,
              dex: "Proven pool",
              protocol: "uniswap_v4",
              quote_symbol: null,
              liquidity_usd: null,
              reserve_token: "123",
              reserve_quote: null,
              created_at: null,
              status: "active",
              is_main: true,
            },
          ],
        }),
      });
    });
    await page.goto(`/token/base/${token}`);
    if (locale === "en")
      await page.getByRole("button", { name: "切换为 English", exact: true }).click();
    await expect(page.locator(".token-header h1")).toBeVisible();
    const panel = page.locator(".token-tabs");
    await panel
      .getByRole("tab", { name: locale === "en" ? "Liquidity" : "流动性", exact: true })
      .click();
    const row = panel.locator(".data-table tbody tr").first();
    await expect(row.getByRole("cell").first()).toContainText("Proven pool · --");
    await expect(row.getByRole("cell").nth(2)).toHaveText("123-- --");
    await expect(row.locator(".address")).toHaveAttribute("title", pool);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    ).toBe(true);
    await page.screenshot({
      path: test.info().outputPath(`token-pool-unknown-quote-${locale}.png`),
      fullPage: true,
    });
    expect(errors).toEqual([]);
  });
}
