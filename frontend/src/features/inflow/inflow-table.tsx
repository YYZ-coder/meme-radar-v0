"use client";
import { useWindowVirtualizer } from "@tanstack/react-virtual";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Icon } from "@/components/icon";
import { useToast } from "@/components/ui/toast";
import type { InflowRow, InflowSortKey, Window } from "@/lib/api/types";
import { tokenKey } from "@/lib/chains";
import { useAppLocale } from "@/lib/locale";
import { useDebounced } from "@/lib/use-debounced";
import { useWatchlist } from "@/lib/watchlist";
import { InflowTableRow, tokenHref } from "./inflow-row";

type SortState = { sort: InflowSortKey; order: "asc" | "desc" };

export function InflowTableHead({
  timeWindow,
  sort,
  onSort,
}: {
  timeWindow: Window;
  sort: SortState;
  onSort?: (key: InflowSortKey) => void;
}) {
  const t = useTranslations("inflow");
  const header = (key: InflowSortKey | null, label: string) => {
    if (!key || !onSort) return <th key={label}>{label}</th>;
    const active = sort.sort === key;
    return (
      <th
        key={label}
        aria-sort={active ? (sort.order === "desc" ? "descending" : "ascending") : undefined}
      >
        <button
          type="button"
          className={active ? "sort-active" : ""}
          aria-label={t("sortBy", { column: label })}
          onClick={() => onSort(key)}
        >
          {label}
          <span aria-hidden="true">{active ? (sort.order === "desc" ? "↓" : "↑") : "↕"}</span>
        </button>
      </th>
    );
  };
  return (
    <thead>
      <tr>
        <th className="star-cell">
          <Icon name="star" size={12} />
        </th>
        {header("age", t("colToken"))}
        {header("sm_holders", t("colSmHolders"))}
        {header("sm_trades", t("colSmTrades"))}
        {header("market_cap", t("colMarketCap"))}
        {header("liquidity", t("colLiquidity"))}
        {header("trades", t("colTrades", { window: timeWindow }))}
        {header("volume", t("colVolume", { window: timeWindow }))}
        {header("net_inflow", t("colNetInflow", { window: timeWindow }))}
        {header(null, t("colTrend", { window: timeWindow }))}
      </tr>
    </thead>
  );
}

/** Virtualized inflow table over the window scroll (14-frontend §7). */
export function InflowTable({
  rows,
  latest,
  missing,
  timeWindow,
  sort,
  onSort,
  onVisibleChange,
  onEndReached,
  hotRows = 0,
}: {
  rows: InflowRow[];
  latest: Map<string, InflowRow>;
  missing: Set<string>;
  timeWindow: Window;
  sort: SortState;
  onSort?: (key: InflowSortKey) => void;
  onVisibleChange: (rows: InflowRow[]) => void;
  onEndReached?: () => void;
  hotRows?: number;
}) {
  const t = useTranslations("inflow");
  const locale = useAppLocale();
  const router = useRouter();
  const toast = useToast();
  const watchlist = useWatchlist();
  const bodyRef = useRef<HTMLTableSectionElement>(null);
  const [margin, setMargin] = useState(0);

  useLayoutEffect(() => {
    const measure = () => {
      const top = bodyRef.current
        ? bodyRef.current.getBoundingClientRect().top + window.scrollY
        : 0;
      setMargin((m) => (Math.abs(m - top) > 1 ? top : m));
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(document.body);
    return () => observer.disconnect();
  }, []);

  const virtualizer = useWindowVirtualizer({
    count: rows.length,
    estimateSize: () => 73,
    overscan: 8,
    scrollMargin: margin,
  });
  const items = virtualizer.getVirtualItems();
  const first = items[0];
  const last = items[items.length - 1];
  const paddingTop = first ? first.start - margin : 0;
  const paddingBottom = last ? virtualizer.getTotalSize() - (last.end - margin) : 0;

  const visibleIndexes = useDebounced(items.map((i) => i.index).join(","), 400);
  const visibleRows = useMemo(
    () =>
      visibleIndexes
        .split(",")
        .filter(Boolean)
        .map((i) => rows[Number(i)])
        .filter(Boolean),
    [visibleIndexes, rows]
  );
  useEffect(() => onVisibleChange(visibleRows), [visibleRows, onVisibleChange]);

  const lastIndex = last?.index ?? -1;
  useEffect(() => {
    if (onEndReached && rows.length && lastIndex >= rows.length - 10) onEndReached();
  }, [lastIndex, rows.length, onEndReached]);

  return (
    <div className="table-scroll">
      <table className="token-table">
        <InflowTableHead timeWindow={timeWindow} sort={sort} onSort={onSort} />
        <tbody ref={bodyRef}>
          {paddingTop > 0 ? (
            <tr aria-hidden="true" className="spacer-row">
              <td colSpan={10} style={{ height: paddingTop }} />
            </tr>
          ) : null}
          {items.map((item) => {
            const base = rows[item.index];
            const key = tokenKey(base.token);
            const row = latest.get(key) ?? base;
            return (
              <InflowTableRow
                key={key}
                index={item.index}
                measureRef={virtualizer.measureElement}
                row={row}
                locale={locale}
                starred={watchlist.has(key)}
                hot={item.index < hotRows}
                stale={missing.has(key)}
                onStar={(r) => {
                  if (!watchlist.toggle(tokenKey(r.token))) toast(t("persistenceUnavailable"));
                }}
                onOpen={(r) => router.push(tokenHref(r))}
              />
            );
          })}
          {paddingBottom > 0 ? (
            <tr aria-hidden="true" className="spacer-row">
              <td colSpan={10} style={{ height: paddingBottom }} />
            </tr>
          ) : null}
        </tbody>
      </table>
    </div>
  );
}
