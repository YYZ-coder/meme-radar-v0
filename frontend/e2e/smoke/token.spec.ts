import { test, expect } from "@playwright/test";

const GX1J = "/token/solana/GX1JGwFmrzgkv6MBjwZZdZt3qBcWi1d5ZuAUS3KFpump";

test("opens token detail from the list", async ({ page }) => {
  await page.goto("/smart-money/inflow");
  const link = page.locator(".token-open").first();
  await expect(link).toBeVisible();
  await link.click();
  await expect(page).toHaveURL(/\/token\/(solana|base|bsc)\//);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
});

test("chart, activity, holders and pools", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(GX1J);
  await expect(page.getByRole("heading", { name: /GX1J/ })).toBeVisible();
  await expect(page.getByTestId("token-chart").locator("canvas").first()).toBeVisible();
  await page.getByRole("group", { name: "周期" }).getByRole("button", { name: "5m" }).click();
  await page.getByRole("button", { name: "MACD", exact: true }).click();
  await expect(page.getByRole("button", { name: "MACD", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true"
  );

  await expect(page.locator(".data-table tbody tr").first()).toBeVisible();
  await page
    .getByRole("button", { name: /^聪明钱/ })
    .first()
    .click();
  await expect(page.locator(".data-table tbody tr").first()).toContainText("聪明钱");

  await page.getByRole("tab", { name: /持有者/ }).click();
  await expect(page.getByRole("tabpanel").getByRole("note")).toContainText("完整覆盖");
  await page.getByRole("tab", { name: "流动性" }).click();
  await expect(page.locator(".data-table")).toContainText("主池");
  expect(errors).toEqual([]);
});

test("partial holder coverage and unknown token", async ({ page }) => {
  await page.goto("/token/base/0x7d4bfd446d4b81d5ede62529f9c8db6fdd81e222");
  await page.getByRole("tab", { name: /持有者/ }).click();
  await expect(page.getByRole("tabpanel").getByRole("note")).toContainText("部分覆盖");
  await page.goto("/token/solana/11111111111111111111111111111111");
  await expect(page.getByRole("heading", { name: "找不到这个代币" })).toBeVisible();
  const res = await page.goto("/token/eth/0x00");
  expect(res?.status()).toBe(404);
});

test("token page fits a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(GX1J);
  await expect(page.getByTestId("token-chart").locator("canvas").first()).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true
  );
});

test("English token tabs fit a phone and remain usable", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/token/bsc/0xc74408c29d7826a937b4d3ae6cc69dbd74004444");
  await page.getByRole("button", { name: "切换为 English", exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByTestId("token-chart").locator("canvas").first()).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true
  );
  await page.getByRole("tab", { name: "Liquidity", exact: true }).click();
  await expect(page.getByRole("tab", { name: "Liquidity", exact: true })).toHaveAttribute(
    "aria-selected",
    "true"
  );
  await expect(page.locator(".data-table")).toBeVisible();
});
