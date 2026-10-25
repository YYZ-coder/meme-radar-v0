import { expect, it, vi } from "vitest";
import { readListBuildChain } from "./list-build-chain";
import type { ListBuildStatus } from "@/lib/api/types";
function status(
  version: string,
  state: ListBuildStatus["build_status"],
  blocking: string | null
): ListBuildStatus {
  return {
    list_version: version,
    previous_version: String(Number(version) - 1),
    active_version: "42",
    status: "building",
    build_status: state,
    blocking_version: blocking,
    created_at: "2026-10-20T10:00:00Z",
    activated_at: null,
    error_code: state === "failed" ? "internal" : null,
    change_counts: { added: 0, merged: 0, removed: 0, total_members: 1 },
  };
}
it("discovers a failed grandparent and identifies that exact retry version", async () => {
  const versions: Record<string, ListBuildStatus> = {
    "45": status("45", "queued", "44"),
    "44": status("44", "queued", "43"),
    "43": status("43", "failed", null),
  };
  const read = vi.fn(async (v: string) => versions[v]);
  const result = await readListBuildChain(read, "45");
  expect(read.mock.calls.map((x) => x[0])).toEqual(["45", "44", "43"]);
  expect(result.selected.list_version).toBe("45");
  expect(result.failed?.list_version).toBe("43");
});
it("rejects a cyclic server queue rather than polling forever", async () => {
  await expect(
    readListBuildChain(async () => status("45", "queued", "45"), "45")
  ).rejects.toMatchObject({ code: "invalid_response" });
});
it("does not issue requests after cancellation", async () => {
  const controller = new AbortController();
  controller.abort();
  const read = vi.fn();
  await expect(readListBuildChain(read, "45", controller.signal)).rejects.toMatchObject({
    name: "AbortError",
  });
  expect(read).not.toHaveBeenCalled();
});
