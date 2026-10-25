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

const connected = process.env.RADAR_E2E_MODE === "connected";

test("mock API answers the contract and flags itself", async ({ request }) => {
  test.skip(connected, "the built-in mock is not used when proxying to the Go API");
  const res = await request.get("/api/v1/market/natives");
  expect(res.status()).toBe(200);
  expect(res.headers()["x-radar-mock"]).toBe("1");
  expect(res.headers()["x-trace-id"]).toMatch(/^[0-9a-f]{32}$/);
  expect((await res.json()).items).toHaveLength(3);
  const denied = await request.get("/api/v1/admin/health");
  expect(denied.status()).toBe(403);
  expect((await denied.json()).error).toMatchObject({ code: "forbidden" });
});

test("connected mode proxies /api/v1 to the Go API and labels demo data", async ({
  page,
  request,
}) => {
  test.skip(!connected, "only meaningful with RADAR_E2E_MODE=connected");
  const res = await request.get("/api/v1/market/natives");
  expect(res.status()).toBe(200);
  // The Go mock store marks its responses; the proxy must keep the marker (D110).
  expect(res.headers()["x-radar-mock"]).toBe("1");
  expect(res.headers()["x-trace-id"]).toMatch(/^[0-9a-f]{32}$/);
  expect((await res.json()).items).toHaveLength(3);
  expect((await request.get("/api/v1/admin/health")).status()).toBe(403);

  // Proof of proxying: an import created directly on the Go process is readable
  // through the frontend, which the built-in mock could not know about.
  const admin = { Authorization: "Bearer mock-admin" };
  const goOrigin = `http://127.0.0.1:${process.env.RADAR_E2E_API_PORT ?? 31437}`;
  const created = await request.post(`${goOrigin}/api/v1/admin/smart-money/imports`, {
    headers: admin,
    multipart: {
      file: {
        name: "probe.csv",
        mimeType: "text/csv",
        buffer: Buffer.from("chain,address\nbase,0x2046d4ee2268e5583be790fde46095bdaf4eaec6\n"),
      },
    },
  });
  expect(created.status()).toBe(201);
  const { id } = await created.json();
  const viaFrontend = await request.get(`/api/v1/admin/smart-money/imports/${id}`, {
    headers: admin,
  });
  expect(viaFrontend.status()).toBe(200);
  expect((await viaFrontend.json()).id).toBe(id);

  await page.goto("/smart-money/inflow");
  await expect(page.locator(".token-table tbody tr[data-index]").first()).toBeVisible();
  await expect(page.getByText("演示数据").filter({ visible: true }).first()).toBeVisible();
});
