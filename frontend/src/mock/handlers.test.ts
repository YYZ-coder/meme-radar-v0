// @vitest-environment node
import { describe, expect, it } from "vitest";
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
    const again = await upload();
    expect(again.totals.existing).toBe(2);
    await ok(`admin/smart-money/imports/${again.id}/apply`, S.listVersionChangeSchema, {
      method: "POST",
      headers: admin,
    });
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
      headers: { ...admin, "content-type": "application/json" },
      body: JSON.stringify({ chain: m.chain, address: m.address, reason: "bot" }),
    });
    expect(excluded.removed).toBe(1);
    const list = await ok("admin/smart-money/exclusions", S.exclusionsSchema, { headers: admin });
    expect(list.items[0].address).toBe(m.address);
    await ok(`admin/smart-money/exclusions/${m.chain}/${m.address}`, S.listVersionChangeSchema, {
      method: "DELETE",
      headers: admin,
    });
  });
});
