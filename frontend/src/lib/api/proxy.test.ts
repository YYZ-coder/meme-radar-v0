import { afterEach, expect, test, vi } from "vitest";
import { proxyAPI } from "./proxy";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

test("preserves the real backend response, query and authorization", async () => {
  const fetcher = vi.fn().mockResolvedValue(
    new Response('{"items":[]}', {
      status: 200,
      headers: { "Content-Type": "application/json", "X-Trace-Id": "backend-trace" },
    })
  );
  vi.stubGlobal("fetch", fetcher);
  const response = await proxyAPI(
    new Request("http://frontend/api/v1/admin/smart-money?chain=base", {
      headers: { Authorization: "Bearer test-only", Cookie: "unrelated=private" },
    }),
    "http://127.0.0.1:20010"
  );
  expect(fetcher.mock.calls[0][0].toString()).toBe(
    "http://127.0.0.1:20010/api/v1/admin/smart-money?chain=base"
  );
  const headers = fetcher.mock.calls[0][1].headers as Headers;
  expect(headers.get("authorization")).toBe("Bearer test-only");
  expect(headers.has("cookie")).toBe(false);
  expect(response.headers.get("X-Trace-Id")).toBe("backend-trace");
  expect(response.headers.has("X-Radar-Mock")).toBe(false);
  expect(await response.json()).toEqual({ items: [] });
});

test("backend connection failure is a typed 503 with no mock fallback or private detail", async () => {
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("private-provider-url")));
  const response = await proxyAPI(
    new Request("http://frontend/api/v1/smart-money/inflow"),
    "http://127.0.0.1:20010"
  );
  expect(response.status).toBe(503);
  expect(response.headers.get("Retry-After")).toBe("5");
  expect(response.headers.has("X-Radar-Mock")).toBe(false);
  const body = await response.json();
  expect(body.error.code).toBe("api_unavailable");
  expect(body.error.trace_id).toMatch(/^[a-f0-9]{32}$/);
  expect(JSON.stringify(body)).not.toContain("private-provider-url");
});

test("oversized bodies are rejected before reaching the backend", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const response = await proxyAPI(
    new Request("http://frontend/api/v1/admin/smart-money/imports", {
      method: "POST",
      body: new Uint8Array(6 * 1024 * 1024 + 1),
    }),
    "http://127.0.0.1:20010"
  );
  expect(response.status).toBe(413);
  expect((await response.json()).error.code).toBe("file_too_large");
  expect(fetcher).not.toHaveBeenCalled();
});

test("an aborted request cancels body reading and never sends a partial import", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const controller = new AbortController();
  controller.abort();
  const request = new Request("http://frontend/api/v1/admin/smart-money/imports", {
    method: "POST",
    body: "not sent",
    signal: controller.signal,
  });
  const response = await proxyAPI(request, "http://127.0.0.1:20010");
  expect(response.status).toBe(503);
  expect(fetcher).not.toHaveBeenCalled();
});

test("no-body retry operations keep their response contract when sent an irrelevant body", async () => {
  const fetcher = vi.fn().mockResolvedValue(new Response("{}", { status: 202 }));
  vi.stubGlobal("fetch", fetcher);
  const response = await proxyAPI(
    new Request("http://frontend/api/v1/admin/parse-failures/example/retry", {
      method: "POST",
      body: new Uint8Array(6 * 1024 * 1024 + 1),
    }),
    "http://127.0.0.1:20010"
  );
  expect(response.status).toBe(202);
  expect(fetcher.mock.calls[0][1].body).toBeUndefined();
});

test("oversized batch bodies retain the declared invalid-param response", async () => {
  vi.stubGlobal("fetch", vi.fn());
  const response = await proxyAPI(
    new Request("http://frontend/api/v1/tokens/batch", {
      method: "POST",
      body: new Uint8Array(6 * 1024 * 1024 + 1),
    }),
    "http://127.0.0.1:20010"
  );
  expect(response.status).toBe(400);
  expect((await response.json()).error.code).toBe("invalid_param");
});

// openapi.yaml: list changes require the Idempotency-Key header end to end.
test("forwards Idempotency-Key and the body of list changes", async () => {
  const fetcher = vi
    .fn()
    .mockImplementation(
      async () => new Response("{}", { status: 202, headers: { "Retry-After": "2" } })
    );
  vi.stubGlobal("fetch", fetcher);
  const key = "0d6f6a8e-3f0b-4b7e-9c1a-2a7b1c9d4e01";
  const post = await proxyAPI(
    new Request("http://frontend/api/v1/admin/smart-money/exclusions", {
      method: "POST",
      headers: { "Idempotency-Key": key, "Content-Type": "application/json" },
      body: '{"reason":"bot"}',
    }),
    "http://127.0.0.1:20010"
  );
  await proxyAPI(
    new Request("http://frontend/api/v1/admin/smart-money/exclusions/solana/abc", {
      method: "DELETE",
      headers: { "Idempotency-Key": key },
    }),
    "http://127.0.0.1:20010"
  );
  for (const call of fetcher.mock.calls) {
    expect((call[1].headers as Headers).get("idempotency-key")).toBe(key);
  }
  expect(new TextDecoder().decode(fetcher.mock.calls[0][1].body)).toBe('{"reason":"bot"}');
  expect(post.status).toBe(202);
  expect(post.headers.get("Retry-After")).toBe("2");
});

// D110: a backend serving demonstration data marks it; the page label must survive the proxy.
test("forwards the upstream demo-data marker", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValue(
        new Response('{"items":[]}', { status: 200, headers: { "X-Radar-Mock": "1" } })
      )
  );
  const response = await proxyAPI(
    new Request("http://frontend/api/v1/market/natives"),
    "http://127.0.0.1:20010"
  );
  expect(response.headers.get("X-Radar-Mock")).toBe("1");
});

// The approved maximum age is one second. Browser callers must receive the
// upstream observation time unchanged, including the age of a cache hit.
test("forwards the actual data observation time and age", async () => {
  vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-10-27T10:00:00.540Z"));
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response('{"as_of":"2026-10-27T10:00:00.000Z"}', {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "X-Data-As-Of": "2026-10-27T10:00:00.123456789Z",
          "X-Data-Age-Ms": "417",
        },
      })
    )
  );
  const response = await proxyAPI(
    new Request("http://frontend/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061"),
    "http://127.0.0.1:20010"
  );
  expect(response.status).toBe(200);
  expect(response.headers.get("X-Data-As-Of")).toBe("2026-10-27T10:00:00.123456789Z");
  expect(response.headers.get("X-Data-Age-Ms")).toBe("417");
  expect(response.headers.get("Cache-Control")).toBe("no-store");
  expect(await response.json()).toEqual({ as_of: "2026-10-27T10:00:00.000Z" });
});

// A successful API observation can expire while the upstream body is delivered.
// The same-origin boundary must not publish it as a fresh successful response.
test("rejects observations that expire during upstream body delivery", async () => {
  const observed = Date.parse("2026-10-27T10:00:00.000Z");
  let now = observed + 500;
  vi.spyOn(Date, "now").mockImplementation(() => now);
  const trace = "1234567890abcdef1234567890abcdef";
  const stream = new ReadableStream<Uint8Array>({
    pull(controller) {
      now = observed + 1100;
      controller.enqueue(new TextEncoder().encode('{"as_of":"2026-10-27T10:00:00.000Z"}'));
      controller.close();
    },
  });
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(stream, {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "X-Data-As-Of": "2026-10-27T10:00:00.000Z",
          "X-Data-Age-Ms": "500",
          "X-Trace-Id": trace,
        },
      })
    )
  );
  const response = await proxyAPI(
    new Request("http://frontend/api/v1/admin/health"),
    "http://127.0.0.1:20010"
  );
  expect(response.status).toBe(503);
  expect(response.headers.has("X-Data-As-Of")).toBe(false);
  expect(response.headers.get("X-Trace-Id")).toBe(trace);
  const body = await response.json();
  expect(body.error.code).toBe("busy");
  expect(body.error.trace_id).toBe(trace);
});

// Regression supplement: delivery age is recomputed without resetting data time.
test("updates delivery age while retaining a valid observation", async () => {
  vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-10-27T10:00:00.700Z"));
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response('{"items":[]}', {
        status: 200,
        headers: { "X-Data-As-Of": "2026-10-27T10:00:00.000Z", "X-Data-Age-Ms": "10" },
      })
    )
  );
  const response = await proxyAPI(
    new Request("http://frontend/api/v1/admin/health"),
    "http://127.0.0.1:20010"
  );
  expect(response.status).toBe(200);
  expect(response.headers.get("X-Data-As-Of")).toBe("2026-10-27T10:00:00.000Z");
  expect(response.headers.get("X-Data-Age-Ms")).toBe("700");
  expect(await response.json()).toEqual({ items: [] });
});

// Live loopback evidence: an age998ms response reached the caller ~3ms later.
// A successful observation must still satisfy the one-second cap after this
// short final handoff; expired payloads cannot be relabeled with a new time.
test("retains the one-second age cap across final response handoff", async () => {
  const at = Date.parse("2026-10-27T10:00:00.000Z");
  let now = at + 998;
  vi.spyOn(Date, "now").mockImplementation(() => now);
  const trace = "1234567890abcdef1234567890abcdef";
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response('{"items":[]}', {
        status: 200,
        headers: { "X-Data-As-Of": "2026-10-27T10:00:00.000Z", "X-Trace-Id": trace },
      })
    )
  );
  const response = await proxyAPI(
    new Request("http://frontend/api/v1/admin/health"),
    "http://127.0.0.1:20010"
  );
  now += 3;
  if (response.status === 200) {
    const ageAtCaller = now - Date.parse(response.headers.get("X-Data-As-Of")!);
    expect(ageAtCaller).toBeLessThanOrEqual(1000);
  } else {
    expect(response.status).toBe(503);
    expect(response.headers.has("X-Data-As-Of")).toBe(false);
    expect(response.headers.get("X-Trace-Id")).toBe(trace);
    expect((await response.json()).error.code).toBe("busy");
  }
});

// Regression supplement: the delivery reserve must not reject a valid960ms read.
test("keeps a 960ms observation available with delivery headroom", async () => {
  vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-10-27T10:00:00.960Z"));
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response('{"items":[]}', {
        status: 200,
        headers: { "X-Data-As-Of": "2026-10-27T10:00:00.000Z" },
      })
    )
  );
  const response = await proxyAPI(
    new Request("http://frontend/api/v1/admin/health"),
    "http://127.0.0.1:20010"
  );
  expect(response.status).toBe(200);
  expect(response.headers.get("X-Data-As-Of")).toBe("2026-10-27T10:00:00.000Z");
  expect(response.headers.get("X-Data-Age-Ms")).toBe("960");
});
