"use client";
import { useSyncExternalStore } from "react";
import { apiFetch, isApiError } from "@/lib/api/client";
import {
  importReportSchema,
  listBuildStatusSchema,
  listVersionChangeSchema,
} from "@/lib/api/schemas";

function sessionSlot(key: string) {
  const event = `${key}:changed`;
  let fallback: string | null = null;
  const read = () => {
    try {
      return sessionStorage.getItem(key) ?? fallback;
    } catch {
      return fallback;
    }
  };
  const subscribe = (listener: () => void) => {
    window.addEventListener(event, listener);
    return () => window.removeEventListener(event, listener);
  };
  return {
    read,
    set(value: string | null) {
      fallback = value;
      try {
        if (value) sessionStorage.setItem(key, value);
        else sessionStorage.removeItem(key);
      } catch {
        /* This tab can still complete the request in memory. */
      }
      window.dispatchEvent(new Event(event));
    },
    useValue() {
      return useSyncExternalStore(subscribe, read, () => null);
    },
  };
}
export const pendingBuild = sessionSlot("meme-radar:pending-list-build");
export const importReport = sessionSlot("meme-radar:import-report");
export const notifiedBuild = sessionSlot("meme-radar:notified-list-build");
const fallbackKeys = new Map<string, string>();
/** A lost response keeps its UUID, so retrying retrieves the accepted version. */
export async function listMutation(
  call: typeof apiFetch,
  method: "POST" | "DELETE",
  path: string,
  body?: unknown
) {
  const fingerprint = JSON.stringify([method, path, body ?? null]);
  const storageKey = `meme-radar:idempotency:${fingerprint}`;
  let key = fallbackKeys.get(fingerprint);
  try {
    key ??= sessionStorage.getItem(storageKey) ?? undefined;
  } catch {
    /* use the tab's in-memory key */
  }
  key ??= crypto.randomUUID();
  fallbackKeys.set(fingerprint, key);
  try {
    sessionStorage.setItem(storageKey, key);
  } catch {
    /* preserve in memory */
  }
  const clear = () => {
    fallbackKeys.delete(fingerprint);
    try {
      sessionStorage.removeItem(storageKey);
    } catch {
      /* nothing to clear */
    }
  };
  try {
    const result = await call(path, listVersionChangeSchema, {
      method,
      headers: { "content-type": "application/json", "Idempotency-Key": key },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    clear();
    return result;
  } catch (error) {
    if (isApiError(error) && error.status >= 400 && error.status < 500) clear();
    throw error;
  }
}

/** A report id recovers an accepted import even when its POST response was lost. */
export async function applyImportRecover(call: typeof apiFetch, id: string) {
  try {
    return await call(`/admin/smart-money/imports/${id}/apply`, listVersionChangeSchema, {
      method: "POST",
    });
  } catch (error) {
    if (!isApiError(error) || (error.status !== 0 && error.status !== 409 && error.status < 500))
      throw error;
    const report = await call(`/admin/smart-money/imports/${id}`, importReportSchema);
    if (!report.applied_list_version) throw error;
    const state = await call(
      `/admin/smart-money/list-builds/${report.applied_list_version}`,
      listBuildStatusSchema
    );
    return {
      list_version: state.list_version,
      previous_version: state.previous_version,
      active_version: state.active_version,
      status: state.status,
      build_status: state.build_status,
      ...state.change_counts,
    };
  }
}
