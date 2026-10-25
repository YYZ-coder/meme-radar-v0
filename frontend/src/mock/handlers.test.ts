// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetAdmin, adminState } from "./admin";
import { activateListBuild, failListBuild, resetListBuilds } from "./list-builds";
import { resetSnapshots, inputVersionRef } from "./inflow";
beforeEach(() => {
  resetAdmin();
  resetListBuilds();
  resetSnapshots();
});
afterEach(() => vi.restoreAllMocks());
import type { z } from "zod";
import * as S from "@/lib/api/schemas";
import { handleMock } from "./handlers";

const BASE = "http://localhost/api/v1/";

async function call(path: string, init: RequestInit = {}) {
  const url = new URL(path, BASE);
  const segments = url.pathname.replace("/api/v1/", "").split("/").filter(Boolean);
  const res = await handleMock(new Request(url, init), segments.map(decodeURIComponent));
  return { status: res.status, headers: res.headers, body: await res.json() };
}

async function ok<T extends z.ZodType>(path: string, schema: T, init?: RequestInit) {
  const res = await call(path, init);
  expect(res.status, `${path} → ${JSON.stringify(res.body).slice(0, 200)}`).toBeLessThan(300);
  expect(res.headers.get("x-radar-mock")).toBe("1");
  expect(res.headers.get("x-trace-id")).toMatch(/^[0-9a-f]{32}$/);
  return schema.parse(res.body) as z.infer<T>;
}

const admin = { authorization: "Bearer mock-admin" };
const GX1J = "solana/GX1JGwFmrzgkv6MBjwZZdZt3qBcWi1d5ZuAUS3KFpump";

describe("mock API conforms to the OpenAPI contract", () => {
  it("reuses an incoming trace id and puts it in error bodies", async () => {
    const id = "4bf92f3577b34da6a3ce929d0e0e4736";
    const res = await call("tokens/solana/11111111111111111111111111111111", {
      headers: { "x-trace-id": id },
    });
    expect(res.headers.get("x-trace-id")).toBe(id);
    expect(S.errorSchema.parse(res.body).error).toMatchObject({ code: "not_found", trace_id: id });
  });

  it("serves every public M1 endpoint with schema-valid bodies", async () => {
    await ok("market/natives", S.nativePricesSchema);
    await ok("search?q=pepe", S.searchSchema);
    await ok("smart-money/overview?window=1h", S.overviewSchema);
    await ok("smart-money/inflow/facets?chain=solana&window=5m", S.facetsSchema);
    const detail = await ok(`tokens/${GX1J}`, S.tokenDetailSchema);
    expect(detail.market_cap).not.toBeNull();
    await ok(`tokens/${GX1J}?currency=native`, S.tokenDetailSchema);
    await ok(`tokens/${GX1J}/candles?interval=1m`, S.candlesSchema);
    await ok(`tokens/${GX1J}/activities?limit=20`, S.activitiesSchema);
    await ok(`tokens/${GX1J}/activities?label=smart_money`, S.activitiesSchema);
    await ok(`tokens/${GX1J}/holders`, S.holdersSchema);
    await ok(`tokens/${GX1J}/pools`, S.poolsSchema);
    await ok(`tokens/${GX1J}/liquidity-events`, S.liquidityEventsSchema);
    await ok(`tokens/${GX1J}/smart-money`, S.tokenSmartMoneySchema);
  });

  it("rejects unsupported candle intervals and unknown tokens", async () => {
    expect((await call(`tokens/${GX1J}/candles?interval=15m`)).status).toBe(400);
    expect((await call("tokens/solana/11111111111111111111111111111111")).status).toBe(404);
    expect((await call("tokens/eth/0x0000000000000000000000000000000000000000")).status).toBe(400);
  });

  it("publishes only closed complete mock candles at its stated cutoff", async () => {
    vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-10-21T12:01:30Z"));
    const response = await ok(
      `tokens/${GX1J}/candles?interval=1m&from=1792584000&to=1792584090`,
      S.candlesSchema
    );
    expect(response.as_of).toBe("2026-10-21T12:01:00.000Z");
    expect(response.coverage).toBe("complete");
    expect(response.observed_items).toEqual([]);
    expect(response.gaps).toEqual([]);
    expect(response.items.map((item) => item.t)).toEqual([1792584000]);
  });

  it("does not classify token-level demo totals as individual buy sizes", async () => {
    const response = await ok("smart-money/overview?chain=base&window=1h", S.overviewSchema);
    expect(response).toMatchObject({
      large_buy_volume_usd: null,
      small_buy_volume_usd: null,
      large_buy_share_pct: null,
      small_buy_share_pct: null,
    });
  });
});

describe("inflow snapshot pagination (Demo 08)", () => {
  it("pages through a fixed order without duplicates", async () => {
    const first = await ok("smart-money/inflow?window=24h&limit=50", S.inflowPageSchema);
    expect(first.items).toHaveLength(50);
    const seen = first.items.map((r) => `${r.token.chain}:${r.token.address}`);
    let offset = first.next_offset;
    while (offset !== null) {
      const next = await ok(
        `smart-money/inflow?query_id=${first.query_id}&offset=${offset}&limit=50`,
        S.inflowPageSchema
      );
      seen.push(...next.items.map((r) => `${r.token.chain}:${r.token.address}`));
      offset = next.next_offset;
    }
    expect(seen).toHaveLength(first.total);
    expect(new Set(seen).size).toBe(seen.length);
  });

  it("sorts descending with nulls last and ties by token id", async () => {
    const page = await ok(
      "smart-money/inflow?window=24h&sort=liquidity&order=asc&limit=100&chain=solana",
      S.inflowPageSchema
    );
    const values = page.items.map((r) => r.liquidity);
    const firstNull = values.indexOf(null);
    if (firstNull >= 0) expect(values.slice(firstNull).every((v) => v === null)).toBe(true);
    const known = values.filter((v): v is string => v !== null).map(Number);
    expect(known).toEqual([...known].sort((a, b) => a - b));
  });

  it("excludes unknown liquidity when min_liquidity is set", async () => {
    const all = await ok(
      "smart-money/inflow?window=24h&limit=100&chain=solana",
      S.inflowPageSchema
    );
    const filtered = await ok(
      "smart-money/inflow?window=24h&limit=100&chain=solana&min_liquidity=10000",
      S.inflowPageSchema
    );
    expect(filtered.items.every((r) => r.liquidity !== null && Number(r.liquidity) >= 10000)).toBe(
      true
    );
    expect(filtered.total).toBeLessThan(all.total);
  });

  it("returns 410 for unknown query ids and 400 for min > max", async () => {
    const gone = await call("smart-money/inflow?query_id=q_missing&offset=50");
    expect(gone.status).toBe(410);
    expect(S.errorSchema.parse(gone.body).error.code).toBe("query_expired");
    const bad = await call("smart-money/inflow?min_volume=10&max_volume=1");
    expect(bad.status).toBe(400);
    expect(bad.body.error.field).toBe("min_volume");
  });

  it("refreshes visible rows by key without reordering", async () => {
    const page = await ok("smart-money/inflow?window=1h&limit=5", S.inflowPageSchema);
    const keys = page.items.map((r) => ({ chain: r.token.chain, address: r.token.address }));
    const batch = await ok("tokens/batch", S.batchSchema, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ window: "1h", tokens: keys }),
    });
    expect(batch.items.map((r) => r.token.address)).toEqual(keys.map((k) => k.address));
  });
});

describe("admin (D106)", () => {
  it("returns 403 without the token and 200 with it", async () => {
    expect((await call("admin/health")).status).toBe(403);
    expect((await call("admin/health", { headers: { authorization: "Bearer nope" } })).status).toBe(
      403
    );
    await ok("admin/health", S.adminHealthSchema, { headers: admin });
  });

  it("imports twice without doubling members", async () => {
    const csv =
      "chain,address,label,tags,twitter,source\n" +
      "solana,7LMk5tKCGXgvweApmksAvfoAVAAXQWGnJdSKTn1bGLqg,Sample,kol|test,@sample,unit\n" +
      "base,0x2046d4ee2268e5583be790fde46095bdaf4eaec6,,,,unit\n" +
      "base,0x2046d4ee2268e5583be790fde46095bdaf4eaec6,,,,unit\n" +
      "bsc,not-an-address,,,,unit\n";
    const upload = async () => {
      const form = new FormData();
      form.set("file", new File([csv], "list.csv", { type: "text/csv" }));
      return ok("admin/smart-money/imports", S.importReportSchema, {
        method: "POST",
        headers: admin,
        body: form,
      });
    };
    const before = await ok("admin/smart-money/members?limit=1", S.membersSchema, {
      headers: admin,
    });
    const report = await upload();
    expect(report.totals).toEqual({ rows: 4, valid: 2, invalid: 1, duplicate: 1, existing: 0 });
    const applied = await ok(
      `admin/smart-money/imports/${report.id}/apply`,
      S.listVersionChangeSchema,
      { method: "POST", headers: admin }
    );
    expect(applied.added).toBe(2);
    expect(
      (
        await call(`admin/smart-money/imports/${report.id}/apply`, {
          method: "POST",
          headers: admin,
        })
      ).status
    ).toBe(409);
    expect(activateListBuild(applied.list_version)).toBe(true);
    const again = await upload();
    expect(again.totals.existing).toBe(2);
    const againApplied = await ok(
      `admin/smart-money/imports/${again.id}/apply`,
      S.listVersionChangeSchema,
      {
        method: "POST",
        headers: admin,
      }
    );
    expect(activateListBuild(againApplied.list_version)).toBe(true);
    const after = await ok("admin/smart-money/members?limit=1", S.membersSchema, {
      headers: admin,
    });
    expect(after.total).toBe(before.total + 2);
    expect(Number(after.list_version)).toBe(Number(before.list_version) + 2);
  });

  it("rejects files that look like they contain keys", async () => {
    const form = new FormData();
    form.set(
      "file",
      new File(["chain,address,private_key\nsolana,abc,xyz\n"], "keys.csv", { type: "text/csv" })
    );
    const res = await call("admin/smart-money/imports", {
      method: "POST",
      headers: admin,
      body: form,
    });
    expect(res.status).toBe(400);
    expect(res.body.error.code).toBe("secret_detected");
  });

  it("excludes and restores an address with new list versions", async () => {
    const members = await ok("admin/smart-money/members?source=auto&limit=1", S.membersSchema, {
      headers: admin,
    });
    const m = members.items[0];
    const excluded = await ok("admin/smart-money/exclusions", S.listVersionChangeSchema, {
      method: "POST",
      headers: {
        ...admin,
        "content-type": "application/json",
        "Idempotency-Key": crypto.randomUUID(),
      },
      body: JSON.stringify({ chain: m.chain, address: m.address, reason: "bot" }),
    });
    expect(excluded.removed).toBe(0);
    expect(excluded.merged).toBe(1);
    expect(activateListBuild(excluded.list_version)).toBe(true);
    const list = await ok("admin/smart-money/exclusions", S.exclusionsSchema, { headers: admin });
    expect(list.items[0].address).toBe(m.address);
    const restored = await ok(
      `admin/smart-money/exclusions/${m.chain}/${m.address}`,
      S.listVersionChangeSchema,
      {
        method: "DELETE",
        headers: { ...admin, "Idempotency-Key": crypto.randomUUID() },
      }
    );
    expect(activateListBuild(restored.list_version)).toBe(true);
  });
});

describe("asynchronous list contract", () => {
  it("returns 202 and keeps the active members while a version prepares", async () => {
    const before = await ok("admin/smart-money/members?limit=1", S.membersSchema, {
      headers: admin,
    });
    const member = before.items[0];
    const pending = await call("admin/smart-money/exclusions", {
      method: "POST",
      headers: {
        ...admin,
        "content-type": "application/json",
        "Idempotency-Key": crypto.randomUUID(),
      },
      body: JSON.stringify({
        chain: member.chain,
        address: member.address,
        reason: "preparing test",
      }),
    });
    expect(pending.status).toBe(202);
    expect(pending.headers.get("retry-after")).toBe("2");
    expect(pending.body).toMatchObject({
      active_version: before.list_version,
      status: "building",
      build_status: "queued",
      removed: 0,
    });
    const during = await ok("admin/smart-money/members?limit=1", S.membersSchema, {
      headers: admin,
    });
    expect(during.list_version).toBe(before.list_version);
    expect(during.items).toEqual(before.items);
  });
});

describe("preparation, retry and frozen query invariants", () => {
  async function exclude(key: string, reason: string) {
    const members = await ok("admin/smart-money/members?limit=1", S.membersSchema, {
      headers: admin,
    });
    const m = members.items[0];
    return call("admin/smart-money/exclusions", {
      method: "POST",
      headers: { ...admin, "content-type": "application/json", "Idempotency-Key": key },
      body: JSON.stringify({ chain: m.chain, address: m.address, reason }),
    });
  }
  it("keeps one version for replayed keys, and rejects changed content", async () => {
    const key = crypto.randomUUID();
    const first = await exclude(key, "same");
    const again = await exclude(key, "same");
    expect(first.status).toBe(202);
    expect(again.body.list_version).toBe(first.body.list_version);
    expect((await exclude(key, "different")).status).toBe(400);
  });
  it("uses the accepted tail, blocks failed parents and retries the same version", async () => {
    const first = await exclude(crypto.randomUUID(), "first");
    const second = await exclude(crypto.randomUUID(), "second");
    expect(second.body.previous_version).toBe(first.body.list_version);
    failListBuild(first.body.list_version);
    expect(activateListBuild(second.body.list_version)).toBe(false);
    const blocked = await ok(
      `admin/smart-money/list-builds/${second.body.list_version}`,
      S.listBuildStatusSchema,
      { headers: admin }
    );
    expect(blocked.blocking_version).toBe(first.body.list_version);
    const retry = await call(`admin/smart-money/list-builds/${first.body.list_version}/retry`, {
      method: "POST",
      headers: admin,
    });
    expect(retry.status).toBe(202);
    expect(retry.body.list_version).toBe(first.body.list_version);
    expect(activateListBuild(first.body.list_version)).toBe(true);
    expect(activateListBuild(second.body.list_version)).toBe(true);
  });
  it("freezes complete page values and as_of, pauses cross-version batch and expires on input revision", async () => {
    const time = Date.parse("2026-10-20T10:00:00Z");
    const clock = vi.spyOn(Date, "now").mockReturnValue(time);
    const first = await ok("smart-money/inflow?window=5m&limit=20", S.inflowPageSchema);
    clock.mockReturnValue(time + 10_000);
    const again = await ok(
      `smart-money/inflow?query_id=${first.query_id}&limit=20`,
      S.inflowPageSchema
    );
    expect(again).toEqual(first);
    const pending = await exclude(crypto.randomUUID(), "switch");
    activateListBuild(pending.body.list_version);
    const oldPage = await ok(
      `smart-money/inflow?query_id=${first.query_id}&limit=20`,
      S.inflowPageSchema
    );
    expect(oldPage).toEqual(first);
    const batch = await call("tokens/batch", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        window: "5m",
        currency: "usd",
        query_id: first.query_id,
        list_version: first.list_version,
        tokens: [first.items[0].token],
      }),
    });
    expect(batch.status).toBe(410);
    inputVersionRef.current++;
    expect((await call(`smart-money/inflow?query_id=${first.query_id}&limit=20`)).status).toBe(410);
  });
  it("recovers an applied report and authenticates before unavailable budget", async () => {
    const form = new FormData();
    form.set(
      "file",
      new File(["chain,address\nbase,0x2046d4ee2268e5583be790fde46095bdaf4eaec6\n"], "one.csv")
    );
    const report = await ok("admin/smart-money/imports", S.importReportSchema, {
      method: "POST",
      headers: admin,
      body: form,
    });
    const change = await ok(
      `admin/smart-money/imports/${report.id}/apply`,
      S.listVersionChangeSchema,
      { method: "POST", headers: admin }
    );
    const restored = await ok(`admin/smart-money/imports/${report.id}`, S.importReportSchema, {
      headers: admin,
    });
    expect(restored.applied_list_version).toBe(change.list_version);
    expect(restored.rows_archived).toBe(false);
    adminState.budgetReady = false;
    expect((await call("admin/health")).status).toBe(403);
    const unavailable = await call("admin/health", { headers: admin });
    expect(unavailable.status).toBe(503);
    expect(unavailable.body.error.code).toBe("api_unavailable");
    expect(unavailable.headers.get("retry-after")).toBe("2");
  });
});
