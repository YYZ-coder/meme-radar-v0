"use client";
import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { Address } from "@/components/ui/address";
import { ChainMark } from "@/components/ui/chain-mark";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState, ErrorState, LoadingState } from "@/components/ui/state";
import { useToast } from "@/components/ui/toast";
import { exclusionsSchema, membersSchema } from "@/lib/api/schemas";
import type { Chain, ListVersionChange, Member } from "@/lib/api/types";
import { chainMeta, chains } from "@/lib/chains";
import { shortAddress } from "@/lib/format/address";
import { formatClock } from "@/lib/format/time";
import { useDebounced } from "@/lib/use-debounced";
import { useAdmin } from "./admin-context";
import { ListBuildNotice } from "./list-build-notice";
import { listMutation, pendingBuild } from "./list-build-state";

export function MembersView() {
  const t = useTranslations("admin");
  const tc = useTranslations("common");
  const { call } = useAdmin();
  const toast = useToast();
  const [chain, setChain] = useState<Chain | "">("");
  const [source, setSource] = useState<"all" | "import" | "auto">("all");
  const [tag, setTag] = useState("");
  const [text, setText] = useState("");
  const q = useDebounced(text.trim(), 300);
  const [excluding, setExcluding] = useState<Member | null>(null);
  const [removing, setRemoving] = useState<Member | null>(null);
  const [reason, setReason] = useState("");

  const members = useInfiniteQuery({
    queryKey: ["admin", "members", chain, source, tag, q],
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) =>
      call("/admin/smart-money/members", membersSchema, {
        signal,
        query: { chain, source, tag: tag.trim(), q, cursor: pageParam, limit: 50 },
      }),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  const exclusions = useQuery({
    queryKey: ["admin", "exclusions"],
    queryFn: ({ signal }) => call("/admin/smart-money/exclusions", exclusionsSchema, { signal }),
  });

  const done = (r: ListVersionChange) => {
    pendingBuild.set(r.list_version);
    toast(t("versionPreparing", { version: r.list_version }));
  };
  const exclude = useMutation({
    mutationFn: (v: { m: Member; reason: string }) =>
      listMutation(call, "POST", "/admin/smart-money/exclusions", {
        chain: v.m.chain,
        address: v.m.address,
        reason: v.reason,
      }),
    onSuccess: (r) => {
      setExcluding(null);
      setReason("");
      done(r);
    },
  });
  const restore = useMutation({
    mutationFn: (v: { chain: Chain; address: string }) =>
      listMutation(call, "DELETE", `/admin/smart-money/exclusions/${v.chain}/${v.address}`),
    onSuccess: done,
  });
  const remove = useMutation({
    mutationFn: (m: Member) =>
      listMutation(call, "DELETE", `/admin/smart-money/members/${m.chain}/${m.address}`),
    onSuccess: (r) => {
      setRemoving(null);
      done(r);
    },
  });

  const first = members.data?.pages[0];
  const rows = members.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <div className="admin-members">
      <ListBuildNotice />
      <div className="admin-toolbar">
        <h3 className="admin-heading">
          {t("membersTitle")}
          {first ? (
            <span className="muted small-note">
              {" "}
              {t("listVersion", { version: first.list_version })} ·{" "}
              {t("total", { count: first.total })}
            </span>
          ) : null}
        </h3>
      </div>
      <div className="admin-filters">
        <select
          aria-label={t("field_chain")}
          value={chain}
          onChange={(e) => setChain(e.target.value as Chain | "")}
        >
          <option value="">{tc("all")}</option>
          {chains.map((c) => (
            <option key={c} value={c}>
              {chainMeta[c].name}
            </option>
          ))}
        </select>
        <select
          aria-label={t("source")}
          value={source}
          onChange={(e) => setSource(e.target.value as typeof source)}
        >
          <option value="all">{t("sourceAll")}</option>
          <option value="import">{t("sourceImport")}</option>
          <option value="auto">{t("sourceAuto")}</option>
        </select>
        <input
          aria-label={t("tag")}
          placeholder={t("tag")}
          value={tag}
          onChange={(e) => setTag(e.target.value)}
        />
        <input
          aria-label={t("query")}
          placeholder={t("query")}
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
      </div>
      {members.isPending ? (
        <LoadingState />
      ) : members.isError ? (
        <ErrorState
          title={t("error", { message: members.error.message })}
          error={members.error}
          onRetry={() => void members.refetch()}
        />
      ) : !rows.length ? (
        <EmptyState title={t("noMembers")} />
      ) : (
        <div className="table-scroll">
          <table className="data-table">
            <thead>
              <tr>
                <th>{t("field_chain")}</th>
                <th>{t("field_address")}</th>
                <th>{t("field_label")}</th>
                <th>{t("field_tags")}</th>
                <th>{t("source")}</th>
                <th>{t("addedAt")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {rows.map((m) => (
                <tr
                  key={`${m.chain}:${m.address}`}
                  className={m.excluded ? "stale-row" : undefined}
                >
                  <td>
                    <ChainMark chain={m.chain} /> {chainMeta[m.chain].name}
                  </td>
                  <td>
                    <Address chain={m.chain} value={m.address} />
                  </td>
                  <td>
                    {m.label ?? "--"}
                    {m.twitter ? <small className="muted"> {m.twitter}</small> : null}
                  </td>
                  <td>{m.tags.join(" | ") || "--"}</td>
                  <td>
                    {m.sources.map((s) => (
                      <span key={s} className="tiny-tag">
                        {s === "import" ? t("sourceImport") : t("sourceAuto")}
                      </span>
                    ))}
                    {m.excluded ? <span className="tiny-tag negative">{t("excluded")}</span> : null}
                  </td>
                  <td className="mono">{formatClock(m.added_at)}</td>
                  <td className="row-actions">
                    {!m.excluded ? (
                      <button
                        type="button"
                        className="outline-button bordered"
                        onClick={() => setExcluding(m)}
                      >
                        {t("exclude")}
                      </button>
                    ) : null}
                    {m.sources.includes("import") ? (
                      <button
                        type="button"
                        className="outline-button bordered"
                        onClick={() => setRemoving(m)}
                      >
                        {t("removeImport")}
                      </button>
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {members.hasNextPage ? (
        <div className="tab-more">
          <button
            type="button"
            className="outline-button bordered"
            onClick={() => void members.fetchNextPage()}
          >
            {tc("loadMore")}
          </button>
        </div>
      ) : null}

      <h3 className="admin-heading">{t("exclusionsTitle")}</h3>
      {exclusions.data?.items.length ? (
        <div className="table-scroll">
          <table className="data-table">
            <tbody>
              {exclusions.data.items.map((e) => (
                <tr key={`${e.chain}:${e.address}`}>
                  <td>
                    <ChainMark chain={e.chain} /> {chainMeta[e.chain].name}
                  </td>
                  <td>
                    <Address chain={e.chain} value={e.address} />
                  </td>
                  <td>{e.reason}</td>
                  <td className="mono">{formatClock(e.created_at)}</td>
                  <td>
                    <button
                      type="button"
                      className="outline-button bordered"
                      disabled={restore.isPending}
                      onClick={() => restore.mutate({ chain: e.chain, address: e.address })}
                    >
                      {t("restore")}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="panel-note">{t("noExclusions")}</p>
      )}

      {excluding ? (
        <Dialog title={t("excludeTitle")} onClose={() => setExcluding(null)}>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              if (reason.trim()) exclude.mutate({ m: excluding, reason: reason.trim() });
            }}
          >
            <p className="muted dialog-description">{t("excludeHint")}</p>
            <p className="mono small-note">{shortAddress(excluding.address, 8, 8)}</p>
            <label className="field-label">
              {t("reason")}
              <input
                value={reason}
                maxLength={200}
                required
                onChange={(e) => setReason(e.target.value)}
              />
            </label>
            {exclude.isError ? <p className="negative">{exclude.error.message}</p> : null}
            <div className="dialog-actions">
              <button type="button" className="outline-button" onClick={() => setExcluding(null)}>
                {tc("cancel")}
              </button>
              <button
                className="primary-button"
                type="submit"
                disabled={!reason.trim() || exclude.isPending}
              >
                {t("exclude")}
              </button>
            </div>
          </form>
        </Dialog>
      ) : null}
      {removing ? (
        <Dialog title={t("removeImport")} onClose={() => setRemoving(null)}>
          <p className="dialog-description">
            {t("removeConfirm", { address: shortAddress(removing.address) })}
          </p>
          {remove.isError ? <p className="negative">{remove.error.message}</p> : null}
          <div className="dialog-actions">
            <button type="button" className="outline-button" onClick={() => setRemoving(null)}>
              {tc("cancel")}
            </button>
            <button
              type="button"
              className="primary-button"
              disabled={remove.isPending}
              onClick={() => remove.mutate(removing)}
            >
              {tc("confirm")}
            </button>
          </div>
        </Dialog>
      ) : null}
    </div>
  );
}
