import { expect, test } from "@playwright/test";

test("partial facets keep unknown totals and label parsed token counts in both languages", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route(/\/api\/v1\/smart-money\/inflow\/facets(?:\?|$)/, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        coverage: "partial",
        dexes: [
          {
            id: "uniswap_v4",
            name: "Uniswap v4",
            chain: "base",
            volume_usd: null,
            token_count: null,
            observed_token_count: 2,
          },
        ],
        platforms: [
          {
            id: "clanker",
            name: "Clanker",
            chain: "base",
            volume_usd: null,
            token_count: null,
            observed_token_count: 2,
          },
        ],
      }),
    });
  });
  await page.goto("/smart-money/inflow?chain=base&window=1m");
  await expect(page.getByRole("note")).toHaveText("筛选计数·覆盖不完整");
  await page.getByRole("button", { name: "全部 DEX", exact: true }).click();
  const dex = page.getByRole("group", { name: "DEX", exact: true });
  await expect(dex.getByText("-- · 已解析 2", { exact: true })).toBeVisible();
  await dex.getByRole("checkbox", { name: "Uniswap v4" }).check();
  await dex.getByRole("button", { name: "确认", exact: true }).click();
  await expect(page).toHaveURL(/dex=uniswap_v4/);
  await page.getByRole("button", { name: "全部平台", exact: true }).click();
  await expect(
    page
      .getByRole("group", { name: "发射平台", exact: true })
      .getByText("-- · 已解析 2", { exact: true })
  ).toBeVisible();
  await page.screenshot({ path: test.info().outputPath("partial-facets-zh.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  const layout = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth,
    elements: Array.from(
      document.querySelectorAll(".toolbar,.toolbar-options,.multi-select,.select-panel")
    ).map((element) => {
      const bounds = element.getBoundingClientRect();
      const style = getComputedStyle(element);
      return {
        class: element.className,
        left: bounds.left,
        right: bounds.right,
        width: bounds.width,
        cssLeft: style.left,
        cssRight: style.right,
        cssWidth: style.width,
      };
    }),
  }));
  await test.info().attach("partial-facets-zh-mobile-layout", {
    body: JSON.stringify(layout, null, 2),
    contentType: "application/json",
  });
  await page.screenshot({
    path: test.info().outputPath("partial-facets-zh-mobile.png"),
    fullPage: true,
  });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true
  );
  const zhPanel = layout.elements.find((element) => element.class === "select-panel");
  expect(zhPanel?.left).toBeGreaterThanOrEqual(0);
  expect(zhPanel?.right).toBeLessThanOrEqual(layout.viewport);
  await page.getByRole("button", { name: "切换为 English" }).click();
  await expect(page.getByRole("note")).toHaveText("Filter counts · incomplete coverage");
  await page.getByRole("button", { name: "All launchpads", exact: true }).click();
  await expect(
    page
      .getByRole("group", { name: "Launchpad", exact: true })
      .getByText("-- · Parsed 2", { exact: true })
  ).toBeVisible();
  const enPanel = await page.getByRole("group", { name: "Launchpad", exact: true }).boundingBox();
  expect(enPanel?.x).toBeGreaterThanOrEqual(0);
  expect(enPanel!.x + enPanel!.width).toBeLessThanOrEqual(390);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true
  );
  await page.screenshot({
    path: test.info().outputPath("partial-facets-en-mobile.png"),
    fullPage: true,
  });
  expect(errors).toEqual([]);
});

test("partial overview shows unknown complete totals with bilingual coverage status", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route(/\/api\/v1\/smart-money\/overview(?:\?|$)/, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
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
      }),
    });
  });
  await page.goto("/smart-money/inflow?chain=base&window=1m");
  await expect(page.getByRole("note")).toHaveText("概览·覆盖不完整");
  const tracked = page.locator(".summary-card").nth(2).locator("strong");
  await expect(tracked).toHaveText(/--\s*TOKENS/);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true
  );
  await page.getByRole("button", { name: "切换为 English" }).click();
  await expect(page.getByRole("note")).toHaveText("Overview · incomplete coverage");
  await expect(tracked).toHaveText(/--\s*TOKENS/);
  await page.screenshot({
    path: test.info().outputPath("partial-overview-en-mobile.png"),
    fullPage: true,
  });
  expect(errors).toEqual([]);
});
