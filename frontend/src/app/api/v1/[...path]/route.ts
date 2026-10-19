// Built-in mock of api/openapi.yaml. When RADAR_API_ORIGIN is set at build time,
// next.config.ts rewrites /api/v1/* to the Go API before this route is reached.
import { handleMock } from "@/mock/handlers";

export const dynamic = "force-dynamic";

type Context = { params: Promise<{ path: string[] }> };

async function handle(request: Request, context: Context) {
  if (process.env.RADAR_API_MOCK === "0" || process.env.RADAR_API_ORIGIN) {
    const traceId = Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) =>
      b.toString(16).padStart(2, "0")
    ).join("");
    return Response.json(
      {
        error: {
          code: "api_unavailable",
          message: "API origin not reachable",
          field: null,
          trace_id: traceId,
        },
      },
      { status: 503, headers: { "Retry-After": "5", "X-Trace-Id": traceId } }
    );
  }
  const { path } = await context.params;
  return handleMock(request, path);
}

export const GET = handle;
export const POST = handle;
export const DELETE = handle;
