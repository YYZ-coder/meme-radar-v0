import { test, expect } from "@playwright/test";

test("legacy /inflow redirects and the page loads without errors", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/inflow");
  await expect(page).toHaveURL(/\/smart-money\/inflow$/);
  await expect(page).toHaveTitle(/Meme Radar/);
  await expect(page.locator(".token-table tbody tr[data-index]").first()).toBeVisible();
  expect(errors).toEqual([]);
});

test("mock API answers the contract and flags itself", async ({ request }) => {
  const res = await request.get("/api/v1/market/natives");
  expect(res.status()).toBe(200);
  expect(res.headers()["x-radar-mock"]).toBe("1");
  expect(res.headers()["x-trace-id"]).toMatch(/^[0-9a-f]{32}$/);
  expect((await res.json()).items).toHaveLength(3);
  const denied = await request.get("/api/v1/admin/health");
  expect(denied.status()).toBe(403);
  expect((await denied.json()).error).toMatchObject({ code: "forbidden" });
});
