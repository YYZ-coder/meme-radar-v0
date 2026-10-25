// Deterministic asynchronous mock of the frozen D -> C1 list handoff.
// This is frontend-only state; PostgreSQL queue/activation belongs to W1.
import type {
  ErrorCode,
  Exclusion,
  ListBuildStatus,
  ListVersionChange,
  Member,
} from "@/lib/api/types";
import { adminState } from "./admin";
import { HttpError, listVersionRef } from "./inflow";

type Draft = { members: Member[]; exclusions: Exclusion[] };
type Counts = Pick<ListVersionChange, "added" | "merged" | "removed" | "total_members">;
type Build = {
  version: string;
  parent: string;
  state: ListBuildStatus["build_status"];
  counts: Counts;
  createdAt: number;
  readyAt: number;
  activatedAt: number | null;
  error: ErrorCode | null;
  draft: Draft;
};
const builds = new Map<string, Build>();
const requests = new Map<string, { fingerprint: string; version: string }>();
let tail = 42;

export function resetListBuilds() {
  builds.clear();
  requests.clear();
  tail = 42;
  listVersionRef.current = 42;
}
export function tailDraft(): Draft {
  return structuredClone(
    builds.get(String(tail))?.draft ?? {
      members: adminState.members,
      exclusions: adminState.exclusions,
    }
  );
}
export function buildStatus(version: string): ListBuildStatus {
  const b = builds.get(version);
  if (!b) throw new HttpError(404, "not_found", "list build not found");
  const active = String(listVersionRef.current);
  return {
    list_version: version,
    previous_version: b.parent,
    active_version: active,
    status: b.state === "activated" ? (version === active ? "active" : "retired") : "building",
    build_status: b.state,
    blocking_version: b.state !== "activated" && b.parent !== active ? b.parent : null,
    created_at: new Date(b.createdAt).toISOString(),
    activated_at: b.activatedAt === null ? null : new Date(b.activatedAt).toISOString(),
    error_code: b.error,
    change_counts: { ...b.counts },
  };
}
export function versionChange(version: string): ListVersionChange {
  const status = buildStatus(version);
  return {
    list_version: version,
    previous_version: status.previous_version,
    active_version: status.active_version,
    status: status.status,
    build_status: status.build_status,
    ...status.change_counts,
  };
}
export function queueListChange(now: number, mutate: (draft: Draft) => Counts): ListVersionChange {
  const draft = tailDraft();
  const counts = mutate(draft);
  const parent = String(tail);
  const version = String(++tail);
  builds.set(version, {
    version,
    parent,
    state: "queued",
    counts,
    createdAt: now,
    readyAt: now + 2000,
    activatedAt: null,
    error: null,
    draft,
  });
  return versionChange(version);
}
/** All checks and mutation happen before enqueue; the same request key is durable in this mock session. */
export function idempotentListChange(
  key: string | null,
  fingerprint: string,
  action: () => ListVersionChange
): ListVersionChange {
  if (
    !key ||
    !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(key)
  )
    throw new HttpError(400, "invalid_param", "UUID Idempotency-Key required", "Idempotency-Key");
  const previous = requests.get(key);
  if (previous) {
    if (previous.fingerprint !== fingerprint)
      throw new HttpError(
        400,
        "invalid_param",
        "idempotency key already identifies another request",
        "Idempotency-Key"
      );
    return versionChange(previous.version);
  }
  const change = action();
  requests.set(key, { fingerprint, version: change.list_version });
  return change;
}
/** Explicit advancement is available to tests; it cannot skip a failed/unfinished parent. */
export function activateListBuild(version: string, now = Date.now()) {
  const b = builds.get(version);
  if (
    !b ||
    b.state === "activated" ||
    b.state === "failed" ||
    b.parent !== String(listVersionRef.current)
  )
    return false;
  b.state = "ready";
  adminState.members = structuredClone(b.draft.members);
  adminState.exclusions = structuredClone(b.draft.exclusions);
  listVersionRef.current = Number(version);
  b.state = "activated";
  b.activatedAt = now;
  return true;
}
export function failListBuild(version: string, code: ErrorCode = "internal") {
  const b = builds.get(version);
  if (!b || b.state === "activated") throw new HttpError(400, "invalid_param", "build cannot fail");
  b.state = "failed";
  b.error = code;
}
export function retryListBuild(version: string, now: number): ListBuildStatus {
  const b = builds.get(version);
  if (!b) throw new HttpError(404, "not_found", "list build not found");
  if (b.state === "failed") {
    b.state = "queued";
    b.error = null;
    b.readyAt = now + 2000;
  }
  return buildStatus(version);
}
/** Controlled clock: browser polling advances the fake worker after two seconds. */
export function tickListBuilds(now: number) {
  for (const b of builds.values()) {
    if (
      b.state === "activated" ||
      b.state === "failed" ||
      b.parent !== String(listVersionRef.current)
    )
      continue;
    if (now >= b.readyAt) activateListBuild(b.version, now);
    else if (now >= b.readyAt - 1500) b.state = "running";
  }
}
