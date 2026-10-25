// The runner explicitly selects either the Go API or the demonstration handlers.
import { handleMock } from "@/mock/handlers";
import { proxyAPI, unavailableAPI } from "@/lib/api/proxy";

export const dynamic = "force-dynamic";

type Context = { params: Promise<{ path: string[] }> };

async function handle(request: Request, context: Context) {
  if (process.env.RADAR_API_ORIGIN) return proxyAPI(request, process.env.RADAR_API_ORIGIN);
  if (process.env.RADAR_API_MOCK === "0") return unavailableAPI();
  const { path } = await context.params;
  return handleMock(request, path);
}

export const GET = handle;
export const POST = handle;
export const DELETE = handle;
