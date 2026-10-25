// @vitest-environment node
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { apiFetch } from "@/lib/api/client";
import { importReportSchema, membersSchema } from "@/lib/api/schemas";
import { resetAdmin } from "@/mock/admin";
import { handleMock } from "@/mock/handlers";
import { resetListBuilds } from "@/mock/list-builds";
import { resetSnapshots } from "@/mock/inflow";
import { applyImportRecover, listMutation } from "./list-build-state";

const headers = { authorization: "Bearer mock-admin" };
const adminCall: typeof apiFetch = (path, schema, init = {}) =>
  apiFetch(path, schema, {
    ...init,
    headers: { ...headers, ...Object.fromEntries(new Headers(init.headers)) },
  });
let lostPath = "";
let lost = false;
const seen: { path: string; method: string; key: string | null }[] = [];
beforeEach(() => {
  resetAdmin();
  resetListBuilds();
  resetSnapshots();
  seen.length = 0;
  lostPath = "";
  lost = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      const request = new Request(url, init);
      const segments = url.pathname.replace("/api/v1/", "").split("/").map(decodeURIComponent);
      seen.push({
        path: url.pathname,
        method: request.method,
        key: request.headers.get("Idempotency-Key"),
      });
      const response = await handleMock(request, segments);
      if (!lost && request.method !== "GET" && url.pathname === lostPath) {
        lost = true;
        throw new TypeError("simulated response loss after commit");
      }
      return response;
    })
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it("recovers an accepted import by report id without submitting a second version", async () => {
  const form = new FormData();
  form.set(
    "file",
    new File(["chain,address\nbase,0x2046d4ee2268e5583be790fde46095bdaf4eaec6\n"], "one.csv")
  );
  const report = await adminCall("/admin/smart-money/imports", importReportSchema, {
    method: "POST",
    body: form,
  });
  lostPath = `/api/v1/admin/smart-money/imports/${report.id}/apply`;
  const log = vi.spyOn(console, "error").mockImplementation(() => {});
  const accepted = await applyImportRecover(adminCall, report.id);
  expect(accepted).toMatchObject({ list_version: "43", active_version: "42", status: "building" });
  expect(seen.filter((r) => r.path === lostPath)).toHaveLength(1);
  expect(log).toHaveBeenCalledWith(
    "api network error",
    expect.objectContaining({
      path: expect.stringContaining("/apply"),
      trace_id: expect.any(String),
    })
  );
});
it("retains the same idempotency UUID after a lost mutation response", async () => {
  const members = await adminCall("/admin/smart-money/members", membersSchema);
  const member = members.items[0];
  lostPath = "/api/v1/admin/smart-money/exclusions";
  const log = vi.spyOn(console, "error").mockImplementation(() => {});
  const body = { chain: member.chain, address: member.address, reason: "lost-response-test" };
  await expect(
    listMutation(adminCall, "POST", "/admin/smart-money/exclusions", body)
  ).rejects.toMatchObject({ status: 0 });
  const accepted = await listMutation(adminCall, "POST", "/admin/smart-money/exclusions", body);
  const posts = seen.filter((r) => r.path === lostPath && r.method === "POST");
  expect(posts).toHaveLength(2);
  expect(posts[0].key).toBe(posts[1].key);
  expect(posts[0].key).toMatch(/^[0-9a-f-]{36}$/);
  expect(accepted.list_version).toBe("43");
  expect(accepted.active_version).toBe("42");
  expect(log).toHaveBeenCalledTimes(1);
});
