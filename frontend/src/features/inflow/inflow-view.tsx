"use client";
import { useQuery } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Icon } from "@/components/icon";
import { MultiSelect } from "@/components/ui/multi-select";
import { EmptyState, ErrorState, LoadingState } from "@/components/ui/state";
import { useToast } from "@/components/ui/toast";
import { SmartMoneyFrame, useRefreshPause } from "@/features/smart-money/frame";
import { apiFetch, isApiError } from "@/lib/api/client";
import { batchSchema } from "@/lib/api/schemas";
import { windows, type InflowRow, type InflowSortKey } from "@/lib/api/types";
import { parseTokenKey, tokenKey } from "@/lib/chains";
import { formatAmount, formatCount } from "@/lib/format/number";
import { formatClock } from "@/lib/format/time";
import { useAppLocale } from "@/lib/locale";
import { usePreferences } from "@/lib/preferences";
import { useUrlParams } from "@/lib/use-url-params";
import { useWatchlist } from "@/lib/watchlist";
import { FilterDialog } from "./filter-dialog";
import { InflowSidebar } from "./inflow-sidebar";
import { InflowTable } from "./inflow-table";
import { useFacets, useInflowList, useRowRefresh } from "./use-inflow";
import {
  countFilters,
  emptyFilters,
  inflowApiQuery,
  inflowParamEntries,
  parseInflowState,
  type InflowState,
} from "./url-state";

export function InflowPage() {
  const [loaded, setLoaded] = useState<InflowRow[]>([]);
  return (
    <SmartMoneyFrame view="inflow" sidebar={<SidebarSlot rows={loaded} />}>
      <InflowBody onRows={setLoaded} />
    </SmartMoneyFrame>
  );
}

function SidebarSlot({ rows }: { rows: InflowRow[] }) {
  const { paused } = useRefreshPause();
  return <InflowSidebar rows={rows} paused={paused} />;
}

function InflowBody({ onRows }: { onRows: (rows: InflowRow[]) => void }) {
  const t = useTranslations("inflow");
  const locale = useAppLocale();
  const toast = useToast();
  const [prefs] = usePreferences();
  const { paused, setPaused } = useRefreshPause();
  const [params, update] = useUrlParams();
  const state = useMemo(() => parseInflowState(new URLSearchParams(params.toString())), [params]);
  const apiQuery = useMemo(() => inflowApiQuery(state, prefs.currency), [state, prefs.currency]);
  const [filterOpen, setFilterOpen] = useState(false);
  const [generation, setGeneration] = useState(0);
  const onExpired = useCallback(() => {
    toast(t("refreshed"));
    window.scrollTo({ top: 0 });
    setGeneration((g) => g + 1);
  }, [toast, t]);
  const facets = useFacets(state.chain, state.window);

  const apply = (patch: Partial<InflowState>) => {
    update(inflowParamEntries({ ...state, ...patch }));
    window.scrollTo({ top: 0 });
  };
  const onSort = (key: InflowSortKey) =>
    apply({ sort: key, order: state.sort === key && state.order === "desc" ? "asc" : "desc" });
  const clearAll = () => apply({ dex: [], platform: [], filters: emptyFilters(), watch: false });
  const filterCount = countFilters(state.filters);

  return (
    <>
      <div className="toolbar">
        <div className="time-selector" role="group" aria-label={t("timeWindow")}>
          {windows.map((w) => (
            <button
              type="button"
              key={w}
              aria-pressed={state.window === w}
              className={state.window === w ? "selected" : ""}
              onClick={() => apply({ window: w })}
            >
              {w}
            </button>
          ))}
        </div>
        <div className="toolbar-options">
          <MultiSelect
            label={t("dex")}
            allLabel={t("dexAll")}
            value={state.dex}
            options={(facets.data?.dexes ?? []).map((d) => ({
              id: d.id,
              label: d.name,
              detail: formatAmount(d.volume_usd, locale),
            }))}
            onChange={(dex) => apply({ dex })}
          />
          <MultiSelect
            label={t("platform")}
            allLabel={t("platformAll")}
            value={state.platform}
            options={(facets.data?.platforms ?? []).map((p) => ({
              id: p.id,
              label: p.name,
              detail: formatCount(p.token_count, locale),
            }))}
            onChange={(platform) => apply({ platform })}
          />
          <button
            type="button"
            className={`filter-button ${filterCount ? "has-filters" : ""}`}
            onClick={() => setFilterOpen(true)}
          >
            <Icon name="filter" size={15} />
            {t("filter")}
            {filterCount ? <span>{filterCount}</span> : null}
          </button>
          <span className="toolbar-separator" />
          <button
            type="button"
            className="icon-button refresh-button"
            aria-label={paused ? t("resume") : t("pause")}
            title={paused ? t("resume") : t("pause")}
            aria-pressed={paused}
            onClick={() => setPaused(!paused)}
          >
            <Icon name={paused ? "play" : "pause"} size={15} />
          </button>
        </div>
      </div>
      {state.watch ? (
        <div className="watchlist-banner">
          <Icon name="star" size={14} />
          {t("watchlistTitle")}
          <span>{t("watchlistHint")}</span>
          <button type="button" onClick={() => apply({ watch: false })}>
            {t("allTokens")}
            <Icon name="arrow" size={13} />
          </button>
        </div>
      ) : null}
      {state.watch ? (
        <WatchlistList
          state={state}
          currency={prefs.currency}
          paused={paused}
          onSort={onSort}
          onRows={onRows}
        />
      ) : (
        <SnapshotList
          key={`${JSON.stringify(apiQuery)}:${generation}`}
          state={state}
          apiQuery={apiQuery}
          generation={generation}
          currency={prefs.currency}
          paused={paused}
          onSort={onSort}
          onRows={onRows}
          onExpired={onExpired}
          onClear={clearAll}
        />
      )}
      {filterOpen ? (
        <FilterDialog
          filters={state.filters}
          onClose={() => setFilterOpen(false)}
          onApply={(filters) => {
            apply({ filters });
            setFilterOpen(false);
          }}
        />
      ) : null}
    </>
  );
}

const emptyMap = new Map<string, InflowRow>();
const emptySet = new Set<string>();

function RefreshStatus({ paused, asOf }: { paused: boolean; asOf: string | null }) {
  const t = useTranslations("inflow");
  return (
    <span className="refresh-status">
      <span className={paused ? "muted" : "status-dot"} />
      {paused ? t("paused") : asOf ? t("updatedAt", { time: formatClock(asOf) }) : t("autoUpdate")}
    </span>
  );
}

function SnapshotList({
  state,
  apiQuery,
  generation,
  currency,
  paused,
  onSort,
  onRows,
  onExpired,
  onClear,
}: {
  state: InflowState;
  apiQuery: Record<string, string>;
  generation: number;
  currency: "usd" | "native";
  paused: boolean;
  onSort: (key: InflowSortKey) => void;
  onRows: (rows: InflowRow[]) => void;
  onExpired: () => void;
  onClear: () => void;
}) {
  const t = useTranslations("inflow");
  const { query, rows, first } = useInflowList(apiQuery, generation, onExpired);
  const [visible, setVisible] = useState<InflowRow[]>([]);
  const keys = useMemo(() => visible.map((r) => r.token), [visible]);
  const { latest, asOf } = useRowRefresh(keys, state.window, currency, !paused && !query.isPending);
  const missing = useMemo(() => {
    const set = new Set<string>();
    if (latest.size) for (const k of keys) if (!latest.has(tokenKey(k))) set.add(tokenKey(k));
    return set;
  }, [latest, keys]);
  const merged = useMemo(() => rows.map((r) => latest.get(tokenKey(r.token)) ?? r), [rows, latest]);
  const onVisible = useCallback((v: InflowRow[]) => setVisible(v), []);
  useEffect(() => onRows(merged), [merged, onRows]);

  const { fetchNextPage, hasNextPage, isFetchingNextPage, isFetchNextPageError } = query;
  const onEnd = useCallback(() => {
    if (hasNextPage && !isFetchingNextPage && !isFetchNextPageError) void fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, isFetchNextPageError, fetchNextPage]);

  if (query.isPending) return <LoadingState />;
  if (query.isError && !rows.length) {
    const err = query.error;
    return (
      <ErrorState
        title={t("error")}
        detail={
          isApiError(err, 429) ? t("rateLimited") : isApiError(err, 503) ? t("busy") : undefined
        }
        error={err}
        onRetry={() => void query.refetch()}
      />
    );
  }
  if (!rows.length) {
    return (
      <EmptyState
        title={t("empty")}
        hint={t("emptyHint")}
        action={
          <button type="button" className="outline-button" onClick={onClear}>
            {t("clearAll")}
          </button>
        }
      />
    );
  }
  const nextError = query.isFetchNextPageError && !isApiError(query.error, 410);
  return (
    <>
      <InflowTable
        rows={rows}
        latest={latest}
        missing={missing}
        timeWindow={state.window}
        sort={{ sort: state.sort, order: state.order }}
        onSort={onSort}
        onVisibleChange={onVisible}
        onEndReached={onEnd}
        hotRows={state.sort === "sm_trades" && state.order === "desc" ? 3 : 0}
      />
      {nextError ? (
        <div className="inline-alert" role="alert">
          {isApiError(query.error, 503)
            ? t("busy")
            : isApiError(query.error, 429)
              ? t("rateLimited")
              : t("error")}
          <button
            type="button"
            className="outline-button"
            onClick={() => void query.fetchNextPage()}
          >
            <Icon name="play" size={12} />
            {t("loadingMore")}
          </button>
        </div>
      ) : null}
      <div className="table-footer">
        <span>
          <span className="small-square" />
          {t("resultCount", { count: first?.total ?? rows.length })}
          {first?.truncated ? <span className="footer-note"> · {t("truncated")}</span> : null}
          <span className="footer-divider">/</span>
          {isFetchingNextPage ? t("loadingMore") : hasNextPage ? `${rows.length}` : t("end")}
        </span>
        <RefreshStatus paused={paused} asOf={asOf} />
      </div>
    </>
  );
}

function WatchlistList({
  state,
  currency,
  paused,
  onSort,
  onRows,
}: {
  state: InflowState;
  currency: "usd" | "native";
  paused: boolean;
  onSort: (key: InflowSortKey) => void;
  onRows: (rows: InflowRow[]) => void;
}) {
  const t = useTranslations("inflow");
  const { keys } = useWatchlist();
  const tokens = useMemo(
    () =>
      keys
        .map(parseTokenKey)
        .filter((k): k is NonNullable<typeof k> => k !== null)
        .filter((k) => !state.chain || k.chain === state.chain),
    [keys, state.chain]
  );
  const list = useQuery({
    queryKey: ["watchlist", state.window, currency, tokens.map(tokenKey).join(",")],
    queryFn: ({ signal }) =>
      apiFetch("/tokens/batch", batchSchema, {
        method: "POST",
        signal,
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ window: state.window, currency, tokens }),
      }),
    enabled: tokens.length > 0,
    refetchInterval: paused ? false : 10_000,
  });
  const rows = useMemo(() => {
    const items = [...(list.data?.items ?? [])];
    const order = state.order === "desc" ? -1 : 1;
    const value = (r: InflowRow): number | null => {
      const v = {
        age: r.age_seconds,
        sm_holders: r.sm_holders,
        sm_trades: r.sm_trades,
        market_cap: r.market_cap,
        liquidity: r.liquidity,
        trades: r.trades,
        volume: r.volume,
        net_inflow: r.net_inflow,
      }[state.sort];
      return v === null ? null : Number(v);
    };
    return items.sort((a, b) => {
      const va = value(a);
      const vb = value(b);
      if (va === null) return 1;
      if (vb === null) return -1;
      return (va - vb) * order;
    });
  }, [list.data, state.sort, state.order]);
  useEffect(() => onRows(rows), [rows, onRows]);
  const noop = useCallback(() => {}, []);

  if (!tokens.length) {
    return <EmptyState title={t("watchlistEmpty")} hint={t("watchlistEmptyHint")} />;
  }
  if (list.isPending) return <LoadingState />;
  if (list.isError) {
    return <ErrorState title={t("error")} error={list.error} onRetry={() => void list.refetch()} />;
  }
  return (
    <>
      <InflowTable
        rows={rows}
        latest={emptyMap}
        missing={emptySet}
        timeWindow={state.window}
        sort={{ sort: state.sort, order: state.order }}
        onSort={onSort}
        onVisibleChange={noop}
      />
      <div className="table-footer">
        <span>
          <span className="small-square" />
          {t("resultCount", { count: rows.length })}
        </span>
        <RefreshStatus paused={paused} asOf={list.data?.as_of ?? null} />
      </div>
    </>
  );
}
