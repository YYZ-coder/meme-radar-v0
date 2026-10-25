// Typed fetch for the same-origin `/api/v1` (proxied to Go, or served by the mock in dev).
import type { z } from "zod";
import { errorSchema } from "./schemas";
import type { ErrorCode } from "./types";

/** Client-side codes for failures that never reached a valid API response. */
export type ClientErrorCode = "network" | "invalid_response";

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: ErrorCode | ClientErrorCode,
    message: string,
    readonly field: string | null = null,
    readonly retryAfter: number | null = null,
    /** Trace id of the request (response header, else the one we sent) — quote it in bug reports. */
    readonly traceId: string | null = null
  ) {
    super(message);
    this.name = "ApiError";
  }
}

/** True when the last response came from the built-in mock API (header x-radar-mock). */
let mockSeen = false;
const mockListeners = new Set<() => void>();
export function subscribeMock(listener: () => void) {
  mockListeners.add(listener);
  return () => {
    mockListeners.delete(listener);
  };
}
export function isMockApi() {
  return mockSeen;
}

/** W3C-compatible trace id (32 hex) sent as X-Trace-Id so browser and server logs line up. */
export function newTraceId(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

type Query = Record<string, string | number | boolean | null | undefined>;

export function buildQuery(query: Query = {}) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === "") continue;
    params.set(key, String(value));
  }
  const text = params.toString();
  return text ? `?${text}` : "";
}

export async function apiFetch<S extends z.ZodType>(
  path: string,
  schema: S,
  init: RequestInit & { query?: Query; token?: string } = {}
): Promise<z.infer<S>> {
  const { query, token, headers, ...rest } = init;
  const finalHeaders = new Headers(headers);
  finalHeaders.set("Accept", "application/json");
  if (token) finalHeaders.set("Authorization", `Bearer ${token}`);
  const sentTrace = newTraceId();
  finalHeaders.set("X-Trace-Id", sentTrace);
  let res: Response;
  try {
    res = await fetch(`/api/v1${path}${buildQuery(query)}`, {
      cache: "no-store",
      ...rest,
      headers: finalHeaders,
    });
  } catch (cause) {
    if (init.signal?.aborted) throw cause;
    const err = new ApiError(0, "network", "network error", null, null, sentTrace);
    console.error("api network error", { path, trace_id: sentTrace });
    throw err;
  }
  const traceId = res.headers.get("x-trace-id") ?? sentTrace;
  if (res.headers.get("x-radar-mock") === "1" && !mockSeen) {
    mockSeen = true;
    mockListeners.forEach((listener) => listener());
  }
  const body: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    const parsed = errorSchema.safeParse(body);
    const retry = Number(res.headers.get("Retry-After"));
    const err = new ApiError(
      res.status,
      parsed.success ? parsed.data.error.code : res.status >= 500 ? "internal" : "invalid_response",
      parsed.success ? parsed.data.error.message : `HTTP ${res.status}`,
      parsed.success ? (parsed.data.error.field ?? null) : null,
      Number.isFinite(retry) && retry > 0 ? retry : null,
      parsed.success ? parsed.data.error.trace_id : traceId
    );
    if (res.status >= 500)
      console.error("api error", { path, code: err.code, trace_id: err.traceId });
    throw err;
  }
  const parsed = schema.safeParse(body);
  if (!parsed.success) {
    console.error("api response failed validation", { path, trace_id: traceId });
    throw new ApiError(res.status, "invalid_response", parsed.error.message, null, null, traceId);
  }
  return parsed.data;
}

export function isApiError(error: unknown, status?: number): error is ApiError {
  return error instanceof ApiError && (status === undefined || error.status === status);
}
