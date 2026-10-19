import { test, expect } from "@playwright/test";

const base58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz";
const randomAddress = () =>
  Array.from({ length: 44 }, () => base58[Math.floor(Math.random() * 58)]).join("");

test("admin token gate, import, exclusion and retry", async ({ page }) => {
  // The mock keeps admin state in memory, so every run uses its own address and label.
  const address = randomAddress();
  const label = `E2E${Math.random().toString(36).slice(2, 8)}`;
  const reason = `bot ${label}`;
  await page.goto("/admin");
  await expect(page).toHaveURL(/\/admin\/health/);
  await page.getByLabel("管理令牌").fill("wrong-token");
  await page.getByRole("button", { name: "进入" }).click();
  await expect(page.locator(".admin-gate [role=alert]")).toContainText("403");

  await page.getByLabel("管理令牌").fill("mock-admin");
  await page.getByRole("button", { name: "进入" }).click();
  await expect(page.getByText("采集进度")).toBeVisible();
  // Mock failures are shared across runs; retry one if any is still failed.
  const retry = page.getByRole("button", { name: "重试", exact: true });
  if (await retry.count()) await retry.first().click();
  await expect(page.getByText("已排队").first()).toBeVisible();

  await page.getByRole("link", { name: "名单导入" }).click();
  await page.getByLabel("选择文件").setInputFiles({
    name: "list.csv",
    mimeType: "text/csv",
    buffer: Buffer.from(
      "chain,address,label,tags,twitter,source\n" +
        `solana,${address},${label},kol,@e2e,e2e\n` +
        "bsc,bad,,,,e2e\n"
    ),
  });
  await page.getByRole("button", { name: "上传并校验" }).click();
  await expect(page.getByText("格式错误 1")).toBeVisible();
  await page.getByRole("button", { name: "确认导入 1 个地址" }).click();
  await expect(page.getByRole("status").filter({ hasText: "已生效" })).toBeVisible();

  await page.getByRole("link", { name: "名单管理" }).click();
  await page.getByLabel("地址或名称").fill(label);
  const member = page.getByRole("row").filter({ hasText: label });
  await expect(member).toHaveCount(1);
  await member.getByRole("button", { name: "排除", exact: true }).click();
  await page.getByRole("dialog").getByLabel("原因").fill(reason);
  await page.getByRole("dialog").getByRole("button", { name: "排除" }).click();
  const excluded = page.getByRole("row").filter({ hasText: reason });
  await expect(excluded).toBeVisible();
  await excluded.getByRole("button", { name: "恢复" }).click();
  await expect(excluded).toHaveCount(0);

  await page.reload();
  await expect(page.getByText("名单管理").first()).toBeVisible();
  await page.getByRole("button", { name: "退出" }).click();
  await expect(page.getByLabel("管理令牌")).toBeVisible();
});
