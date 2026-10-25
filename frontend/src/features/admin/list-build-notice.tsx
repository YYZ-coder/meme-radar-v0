"use client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useTranslations } from "next-intl";
import { ErrorState } from "@/components/ui/state";
import { readListBuildChain } from "./list-build-chain";
import { useToast } from "@/components/ui/toast";
import { listBuildStatusSchema } from "@/lib/api/schemas";
import { useAdmin } from "./admin-context";
import { notifiedBuild, pendingBuild } from "./list-build-state";

export function ListBuildNotice({ version: reportVersion }: { version?: string | null } = {}) {
  const rememberedVersion = pendingBuild.useValue();
  const version = reportVersion ?? rememberedVersion;
  const { call } = useAdmin();
  const t = useTranslations("admin");
  const client = useQueryClient();
  const toast = useToast();
  const query = useQuery({
    queryKey: ["admin", "list-build", version],
    queryFn: ({ signal }) =>
      readListBuildChain(
        (v, signal) =>
          call(`/admin/smart-money/list-builds/${v}`, listBuildStatusSchema, { signal }),
        version!,
        signal
      ),
    enabled: version !== null,
    refetchInterval: (q) => (q.state.data?.selected.build_status === "activated" ? false : 2000),
    refetchIntervalInBackground: false,
  });
  const status = query.data?.selected;
  const failedBuild = query.data?.failed;
  const completed = status?.build_status === "activated";
  useEffect(() => {
    if (!completed || !version || notifiedBuild.read() === version) return;
    notifiedBuild.set(version);
    void client.invalidateQueries({ queryKey: ["admin", "members"] });
    void client.invalidateQueries({ queryKey: ["admin", "exclusions"] });
    void client.invalidateQueries({ queryKey: ["admin", "import-report"] });
    toast(t("versionActivated", { version }));
  }, [completed, version, client, toast, t]);
  const retry = useMutation({
    mutationFn: () =>
      call(
        `/admin/smart-money/list-builds/${failedBuild?.list_version ?? version}/retry`,
        listBuildStatusSchema,
        { method: "POST" }
      ),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["admin", "list-build"] });
    },
  });
  if (!version) return null;
  const failed = !!failedBuild;
  return (
    <div
      className={`inline-alert ${failed ? "negative" : completed ? "positive" : "muted"}`}
      role="status"
      aria-live="polite"
    >
      <span>
        {completed && status
          ? t("applied", {
              added: status.change_counts.added,
              merged: status.change_counts.merged,
              version,
            })
          : failed
            ? t("buildFailed", { version: failedBuild?.list_version ?? version })
            : t("versionPreparing", { version })}
      </span>
      {status?.blocking_version ? (
        <span>
          {t("buildBlocked", { version: failedBuild?.list_version ?? status.blocking_version })}
        </span>
      ) : null}
      {query.isError ? <ErrorState title={t("requestFailed")} error={query.error} /> : null}
      {retry.isError ? <ErrorState title={t("requestFailed")} error={retry.error} /> : null}
      {failed ? (
        <button
          className="outline-button"
          type="button"
          disabled={retry.isPending}
          onClick={() => retry.mutate()}
        >
          {t("retry")}
        </button>
      ) : null}
      {query.isError ? (
        <button className="outline-button" type="button" onClick={() => void query.refetch()}>
          {t("retry")}
        </button>
      ) : null}
    </div>
  );
}
