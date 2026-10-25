import { ApiError } from "@/lib/api/client";
import type { ListBuildStatus } from "@/lib/api/types";

/** Follow the frozen immediate-parent relation until its failed or runnable head. */
export async function readListBuildChain(
  read: (version: string, signal?: AbortSignal) => Promise<ListBuildStatus>,
  version: string,
  signal?: AbortSignal
) {
  signal?.throwIfAborted();
  const selected = await read(version, signal);
  const seen = new Set<string>();
  let current = selected;
  for (;;) {
    signal?.throwIfAborted();
    if (seen.has(current.list_version))
      throw new ApiError(502, "invalid_response", "cyclic list queue");
    seen.add(current.list_version);
    if (current.build_status === "failed") return { selected, failed: current };
    if (!current.blocking_version) return { selected, failed: null };
    current = await read(current.blocking_version, signal);
  }
}
