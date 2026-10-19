import { test, expect, type Page } from "@playwright/test";

const rows = (page: Page) => page.locator(".token-table tbody tr[data-index]");

test.beforeEach(async ({ context }) => {
  await context.clearCookies();
});

test("chain, window, sort and filters live in the URL", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/smart-money/inflow");
  await expect(rows(page).first()).toBeVisible();
  await expect(page.getByRole("columnheader", { name: /1h 净流入/ })).toBeVisible();

  await page.getByRole("button", { name: "Base", exact: true }).click();
  await expect(page).toHaveURL(/chain=base/);
  await page.getByRole("button", { name: "5m", exact: true }).click();
  await expect(page).toHaveURL(/window=5m/);
  await expect(page.getByRole("columnheader", { name: /5m 净流入/ })).toBeVisible();

  await page.getByRole("button", { name: "按流动性排序" }).click();
  await expect(page).toHaveURL(/sort=liquidity/);

  await page.getByRole("button", { name: "筛选", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("流动性 (USD) 最小").fill("1000");
  await dialog.getByLabel("交易额 (USD) 最小").fill("50");
  await dialog.getByLabel("交易额 (USD) 最大").fill("10");
  await expect(dialog.getByText("最小值不能大于最大值")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "确认" })).toBeDisabled();
  await dialog.getByLabel("交易额 (USD) 最大").fill("");
  await dialog.getByRole("button", { name: "确认" }).click();
  await expect(page).toHaveURL(/min_liquidity=1000/);
  await expect(page.getByRole("button", { name: /^筛选\s*2$/ })).toBeVisible();

  await page.reload();
  await expect(page.getByRole("button", { name: "Base", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true"
  );
  await expect(page.getByRole("button", { name: "5m", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true"
  );
  await expect(rows(page).first()).toBeVisible();
  expect(errors).toEqual([]);
});

test("infinite scroll pages with query_id and recovers from 410", async ({ page }) => {
  let pages = 0;
  page.on("response", (res) => {
    if (/smart-money\/inflow\?query_id=/.test(res.url()) && res.status() === 200) pages += 1;
  });
  let expired = false;
  await page.route(/\/api\/v1\/smart-money\/inflow\?query_id=/, async (route) => {
    if (!expired) {
      expired = true;
      await route.fulfill({
        status: 410,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "query_expired",
            message: "expired",
            trace_id: "0123456789abcdef0123456789abcdef",
          },
        }),
      });
    } else await route.continue();
  });
  await page.goto("/smart-money/inflow?window=24h");
  await expect(rows(page).first()).toBeVisible();
  await page.mouse.wheel(0, 6000);
  await expect(page.getByText("列表已过期，已重新查询")).toBeVisible();
  await expect(rows(page).first()).toBeVisible();
  // After the re-query, paging continues with the new query_id.
  await expect
    .poll(
      async () => {
        await page.mouse.wheel(0, 4000);
        return pages;
      },
      { timeout: 15_000 }
    )
    .toBeGreaterThan(0);
});

test("keeps loaded rows and offers retry on 503", async ({ page }) => {
  let fail = true;
  await page.route(/\/api\/v1\/smart-money\/inflow\?/, async (route) => {
    if (fail && !route.request().url().includes("query_id")) {
      await route.fulfill({
        status: 503,
        headers: { "Retry-After": "2", "X-Trace-Id": "0123456789abcdef0123456789abcdef" },
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "busy", message: "busy", trace_id: "0123456789abcdef0123456789abcdef" },
        }),
      });
    } else await route.continue();
  });
  await page.goto("/smart-money/inflow");
  await expect(page.locator(".state-block.error")).toContainText("服务繁忙");
  await expect(page.locator(".state-block.error")).toContainText(
    "0123456789abcdef0123456789abcdef"
  );
  fail = false;
  await page.getByRole("button", { name: "重试" }).click();
  await expect(rows(page).first()).toBeVisible();
});

test("watchlist persists in the browser and shows unknown values as --", async ({ page }) => {
  await page.goto("/smart-money/inflow?chain=solana&window=24h&sort=liquidity&order=asc");
  await expect(rows(page).first()).toBeVisible();
  await page
    .getByRole("button", { name: /^关注 / })
    .first()
    .click();
  await page.getByRole("link", { name: "观察列表" }).click();
  await expect(page).toHaveURL(/watch=1/);
  await expect(rows(page)).toHaveCount(1);
  await page.reload();
  await expect(rows(page)).toHaveCount(1);
  await page.getByRole("button", { name: /^取消关注 / }).click();
  await expect(page.getByRole("heading", { name: "还没有关注的代币" })).toBeVisible();
});

test("language switch is remembered by cookie", async ({ page }) => {
  await page.goto("/smart-money/inflow");
  await page.getByRole("button", { name: "切换为 English" }).click();
  await expect(
    page.getByRole("heading", { name: "Find the next onchain opportunity" })
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Find the next onchain opportunity" })
  ).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await page.getByRole("button", { name: "切换为简体中文" }).click();
  await expect(page.getByRole("heading", { name: "发现链上下一次机会" })).toBeVisible();
});

test("global search opens a token", async ({ page }) => {
  await page.goto("/smart-money/inflow");
  await expect(rows(page).first()).toBeVisible();
  await page.keyboard.press("/");
  await expect(
    page.getByRole("combobox", { name: "搜索代币 / 合约 / 钱包地址" }).first()
  ).toBeFocused();
  await page.keyboard.type("GX1J");
  await page.getByRole("option", { name: /GX1J/ }).first().click();
  await expect(page).toHaveURL(/\/token\/solana\/GX1J/);
});

test("mobile layout stays within the viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/smart-money/inflow");
  await expect(page.getByRole("heading", { name: "发现链上下一次机会" })).toBeVisible();
  await expect(rows(page).first()).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true
  );
  await page.getByRole("button", { name: "筛选", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
});
