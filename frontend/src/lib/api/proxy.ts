// Server-only same-origin forwarding. A failed upstream remains an API error;
// it must never select the built-in demonstration handlers.
const maximumObservationAgeMs = 1000;
// Leave room for the local socket handoff after this response is constructed.
const finalDeliveryReserveMs = 10;

export async function proxyAPI(request: Request, origin: string): Promise<Response> {
  try {
    const signal = AbortSignal.any([request.signal, AbortSignal.timeout(10_000)]);
    signal.throwIfAborted();
    const incoming = new URL(request.url);
    const target = new URL(incoming.pathname + incoming.search, origin);
    const hasBody =
      request.method === "POST" &&
      [
        "/api/v1/admin/smart-money/imports",
        "/api/v1/tokens/batch",
        "/api/v1/admin/smart-money/exclusions",
      ].includes(incoming.pathname);
    const headers = new Headers();
    for (const name of [
      "authorization",
      "content-type",
      "accept",
      "traceparent",
      "x-trace-id",
      "idempotency-key",
    ]) {
      const value = request.headers.get(name);
      if (value) headers.set(name, value);
    }
    const upstream = await fetch(target, {
      method: request.method,
      headers,
      body: hasBody ? await readBody(request, signal) : undefined,
      redirect: "manual",
      cache: "no-store",
      signal,
    });
    const responseHeaders = new Headers({ "Cache-Control": "no-store" });
    for (const name of [
      "content-type",
      "retry-after",
      "x-trace-id",
      "x-radar-mock",
      "x-data-as-of",
      "x-data-age-ms",
    ]) {
      const value = upstream.headers.get(name);
      if (value) responseHeaders.set(name, value);
    }
    const observed = upstream.headers.get("x-data-as-of");
    if (upstream.status === 200 && observed) {
      // These small health/detail responses must still satisfy the age cap
      // after the upstream body is delivered, not only when its headers arrive.
      const body = await upstream.arrayBuffer();
      const at = Date.parse(observed);
      const age = Date.now() - at;
      if (!Number.isFinite(at) || age + finalDeliveryReserveMs > maximumObservationAgeMs) {
        return errorAPI(
          503,
          "busy",
          "data observation expired",
          upstream.headers.get("x-trace-id")
        );
      }
      responseHeaders.set("X-Data-Age-Ms", String(Math.max(0, age)));
      return new Response(body, { status: upstream.status, headers: responseHeaders });
    }
    return new Response(upstream.body, { status: upstream.status, headers: responseHeaders });
  } catch (error) {
    if (error instanceof BodyTooLarge) {
      const upload = new URL(request.url).pathname === "/api/v1/admin/smart-money/imports";
      return upload
        ? errorAPI(413, "file_too_large", "request exceeds upload limit")
        : errorAPI(400, "invalid_param", "request body exceeds limit");
    }
    return unavailableAPI();
  }
}

export function unavailableAPI(): Response {
  return errorAPI(503, "api_unavailable", "API origin not reachable");
}

function errorAPI(
  status: number,
  code: string,
  message: string,
  originalTraceId?: string | null
): Response {
  const traceId =
    originalTraceId && /^[a-f0-9]{32}$/.test(originalTraceId)
      ? originalTraceId
      : crypto.randomUUID().replaceAll("-", "");
  return Response.json(
    {
      error: {
        code,
        message,
        field: null,
        trace_id: traceId,
      },
    },
    {
      status,
      headers: {
        ...(status === 503 ? { "Retry-After": "5" } : {}),
        "X-Trace-Id": traceId,
        "Cache-Control": "no-store",
      },
    }
  );
}

class BodyTooLarge extends Error {}

async function readBody(request: Request, signal: AbortSignal): Promise<ArrayBuffer | undefined> {
  // A file may be 5 MiB; allow bounded multipart framing around it. The Go API
  // still enforces the exact file/row limit. Bound reads even without a length.
  const maximum = 6 * 1024 * 1024;
  if (Number(request.headers.get("content-length")) > maximum) throw new BodyTooLarge();
  if (!request.body) return undefined;
  const reader = request.body.getReader();
  const cancel = () => {
    void reader.cancel(signal.reason).catch(() => {});
  };
  signal.addEventListener("abort", cancel, { once: true });
  let length = 0;
  const chunks: Uint8Array[] = [];
  try {
    for (;;) {
      signal.throwIfAborted();
      const { done, value } = await reader.read();
      signal.throwIfAborted();
      if (done) break;
      length += value.length;
      if (length > maximum) {
        await reader.cancel();
        throw new BodyTooLarge();
      }
      chunks.push(value);
    }
    const body = new Uint8Array(length);
    let offset = 0;
    for (const chunk of chunks) {
      body.set(chunk, offset);
      offset += chunk.length;
    }
    return body.buffer;
  } finally {
    signal.removeEventListener("abort", cancel);
    reader.releaseLock();
  }
}
