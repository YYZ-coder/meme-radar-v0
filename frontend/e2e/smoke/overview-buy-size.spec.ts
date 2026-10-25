import { expect, test } from "@playwright/test";

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

test("market pulse displays published buy-size amounts and shares in both languages", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route(/\/api\/v1\/smart-money\/overview(?:\?|$)/, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(complete),
    });
  });
  await page.goto("/smart-money/inflow?chain=base&window=1m");
  const zh = page.getByRole("group", { name: "买入金额分布", exact: true });
  await expect(zh.getByText("60.00%", { exact: true })).toBeVisible();
  await expect(zh.getByText("40.00%", { exact: true })).toBeVisible();
  await expect(zh.getByText("$6,000", { exact: true })).toBeVisible();
  await expect(zh.getByText("$4,000", { exact: true })).toBeVisible();
  await expect(zh).toHaveAttribute("title", /小于或等于 5,000 美元/);
  await expect(zh.getByRole("img")).toHaveAccessibleName(/60\.00%.*40\.00%/);
  await page
    .getByRole("region", { name: "市场脉搏", exact: true })
    .screenshot({ path: test.info().outputPath("buy-size-zh.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "切换为 English" }).click();
  const en = page.getByRole("group", { name: "Buy volume by position", exact: true });
  await expect(en.getByText("60.00%", { exact: true })).toBeVisible();
  await expect(en.getByText("40.00%", { exact: true })).toBeVisible();
  await expect(en.getByText("$6.00K", { exact: true })).toBeVisible();
  await expect(en.getByText("$4.00K", { exact: true })).toBeVisible();
  await expect(en).toHaveAttribute("title", /US\$5,000 or less is small/);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true
  );
  await page
    .getByRole("region", { name: "Market pulse", exact: true })
    .screenshot({ path: test.info().outputPath("buy-size-en-mobile.png") });
  expect(errors).toEqual([]);
});

test("market pulse distinguishes incomplete buys from a known empty buy window", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  let empty = false;
  await page.route(/\/api\/v1\/smart-money\/overview(?:\?|$)/, async (route) => {
    const body = empty
      ? {
          ...complete,
          net_inflow_usd: "0",
          buy_volume_usd: "0",
          buy_share_pct: null,
          large_buy_volume_usd: "0",
          small_buy_volume_usd: "0",
          large_buy_share_pct: null,
          small_buy_share_pct: null,
        }
      : {
          ...complete,
          coverage: "partial",
          net_inflow_usd: null,
          buy_volume_usd: null,
          buy_share_pct: null,
          large_buy_volume_usd: null,
          small_buy_volume_usd: null,
          large_buy_share_pct: null,
          small_buy_share_pct: null,
        };
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(body),
    });
  });
  await page.goto("/smart-money/inflow?chain=base&window=1m");
  const split = page.getByRole("group", { name: "买入金额分布", exact: true });
  await expect(split.getByText("统计待补齐", { exact: true })).toBeVisible();
  await expect(split.getByText("--", { exact: true })).toHaveCount(4);
  await expect(split.getByRole("img")).toHaveCount(0);
  empty = true;
  await page.reload();
  await expect(split.getByText("暂无买入", { exact: true })).toBeVisible();
  await expect(split.getByText("$0", { exact: true })).toHaveCount(2);
  await expect(split.getByText("--", { exact: true })).toHaveCount(2);
  await expect(split.getByRole("img")).toHaveCount(0);
  expect(errors).toEqual([]);
});
