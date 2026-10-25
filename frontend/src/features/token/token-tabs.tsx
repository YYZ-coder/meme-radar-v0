"use client";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { Address } from "@/components/ui/address";
import { EmptyState, ErrorState, LoadingState } from "@/components/ui/state";
import { apiFetch } from "@/lib/api/client";
import {
  activitiesSchema,
  holdersSchema,
  liquidityEventsSchema,
  poolsSchema,
} from "@/lib/api/schemas";
import type { ActivityLabelFilter, Chain, TokenDetail, WalletRef } from "@/lib/api/types";
import { chainMeta, explorerUrl } from "@/lib/chains";
import { shortAddress } from "@/lib/format/address";
import {
  formatAmount,
  formatCount,
  formatPercent,
  formatPrice,
  formatQuantity,
  UNKNOWN,
} from "@/lib/format/number";
import { formatAge, formatClock, secondsSince, timeTitle } from "@/lib/format/time";
import { useAppLocale } from "@/lib/locale";
import { Icon } from "@/components/icon";

type Tab = "activity" | "holders" | "liquidity";

const m1Labels = ["all", "smart_money"] as const satisfies readonly ActivityLabelFilter[];
const m3Labels: ActivityLabelFilter[] = ["kol", "insider", "whale", "sniper", "dev", "bundler"];
const labelKey: Record<ActivityLabelFilter, string> = {
  all: "labelAll",
  smart_money: "labelSmartMoney",
  kol: "labelKol",
  insider: "labelInsider",
  whale: "labelWhale",
  sniper: "labelSniper",
  dev: "labelDev",
  bundler: "labelBundler",
};

function Wallet({ chain, wallet }: { chain: Chain; wallet: WalletRef | null }) {
  const t = useTranslations("token");
  if (!wallet) return <span className="muted">{t("unattributed")}</span>;
  return (
    <span className="wallet-cell">
      <Address chain={chain} value={wallet.address} label={wallet.display_name ?? undefined} />
      {wallet.labels.includes("smart_money") ? (
        <span className="tiny-tag">{t("labelSmartMoney")}</span>
      ) : null}
      {wallet.labels.includes("kol") ? <span className="tiny-tag kol">{t("labelKol")}</span> : null}
    </span>
  );
}

function TxLink({ chain, hash }: { chain: Chain; hash: string }) {
  const tc = useTranslations("common");
  return (
    <a
      className="tx-link mono"
      href={explorerUrl(chain, "tx", hash)}
      target="_blank"
      rel="noreferrer noopener"
      aria-label={tc("explorer")}
    >
      {shortAddress(hash, 4, 4)}
      <Icon name="external" size={10} />
    </a>
  );
}

function MoreButton({
  hasMore,
  loading,
  onMore,
}: {
  hasMore: boolean;
  loading: boolean;
  onMore: () => void;
}) {
  const tc = useTranslations("common");
  return (
    <div className="tab-more">
      {hasMore ? (
        <button type="button" className="outline-button" disabled={loading} onClick={onMore}>
          {loading ? tc("loading") : tc("loadMore")}
        </button>
      ) : (
        <span className="muted">{tc("noMore")}</span>
      )}
    </div>
  );
}

function ActivityTab({ chain, address }: { chain: Chain; address: string }) {
  const t = useTranslations("token");
  const locale = useAppLocale();
  const [label, setLabel] = useState<ActivityLabelFilter>("all");
  const [kind, setKind] = useState<"all" | "trade" | "liquidity">("all");
  const q = useInfiniteQuery({
    queryKey: ["activities", chain, address, label, kind],
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) =>
      apiFetch(`/tokens/${chain}/${address}/activities`, activitiesSchema, {
        signal,
        query: { label, kind, cursor: pageParam, limit: 50 },
      }),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    refetchInterval: 10_000,
  });
  const rows = q.data?.pages.flatMap((p) => p.items) ?? [];
  const counts = q.data?.pages[0]?.label_counts;
  const partialFeed = q.data?.pages.some((page) => page.coverage === "partial") ?? false;
  const partialCounts = q.data?.pages[0]?.label_counts_coverage === "partial";
  return (
    <>
      <div className="tab-filters">
        <div className="chip-row" role="group" aria-label={t("colTrader")}>
          {m1Labels.map((l) => (
            <button
              type="button"
              key={l}
              aria-pressed={label === l}
              className={`chip ${label === l ? "selected" : ""}`}
              onClick={() => setLabel(l)}
            >
              {t(labelKey[l])}
              <small>{counts ? formatCount(counts[l], locale) : ""}</small>
            </button>
          ))}
          {m3Labels.map((l) => (
            <button type="button" key={l} className="chip" disabled title={t("labelsM3")}>
              {t(labelKey[l])}
            </button>
          ))}
        </div>
        <div className="segmented" role="group" aria-label={t("colType")}>
          {(["all", "trade", "liquidity"] as const).map((k) => (
            <button
              type="button"
              key={k}
              aria-pressed={kind === k}
              className={kind === k ? "selected" : ""}
              onClick={() => setKind(k)}
            >
              {t(k === "all" ? "filterAll" : k === "trade" ? "filterTrades" : "filterLiquidity")}
            </button>
          ))}
        </div>
      </div>
      {partialFeed ? (
        <p className="panel-note" role="note" title={t("activityPartialHint")}>
          {t("activityPartial")}
        </p>
      ) : null}
      {partialCounts ? (
        <p className="panel-note" role="note" title={t("activityCountsPartialHint")}>
          {t("activityCountsPartial")}
        </p>
      ) : null}
      {q.isPending ? (
        <LoadingState />
      ) : q.isError && !rows.length ? (
        <ErrorState title={t("error")} error={q.error} onRetry={() => void q.refetch()} />
      ) : !rows.length ? (
        <EmptyState title={t("tabActivity")} />
      ) : (
        <div className="table-scroll">
          <table className="data-table">
            <thead>
              <tr>
                <th>{t("colTime")}</th>
                <th>{t("colType")}</th>
                <th>{t("colMarketCap")}</th>
                <th>{t("colAmount")}</th>
                <th>{t("colTotal")}</th>
                <th>{t("colTrader")}</th>
                <th>{t("colTx")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((a) => {
                const buyish = a.kind === "buy" || a.kind === "add";
                return (
                  <tr key={a.id}>
                    <td className="mono" title={timeTitle(a.time)}>
                      {formatAge(secondsSince(a.time))}
                      <small>{formatClock(a.time)}</small>
                    </td>
                    <td className={buyish ? "positive" : "negative"}>
                      {t(a.kind)}
                      {a.attribution === "inferred" ? (
                        <small className="muted"> · {t("inferred")}</small>
                      ) : null}
                    </td>
                    <td className="mono">{formatAmount(a.market_cap_usd, locale)}</td>
                    <td className="mono">
                      {formatQuantity(a.token_amount, locale)}
                      <small>
                        {formatQuantity(a.quote_amount, locale)} {a.quote_symbol ?? UNKNOWN}
                      </small>
                    </td>
                    <td className={`mono ${buyish ? "positive" : "negative"}`}>
                      {formatAmount(a.value_usd, locale)}
                    </td>
                    <td>
                      <Wallet chain={chain} wallet={a.wallet} />
                    </td>
                    <td>
                      <TxLink chain={chain} hash={a.tx_hash} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {rows.length ? (
        <MoreButton
          hasMore={q.hasNextPage}
          loading={q.isFetchingNextPage}
          onMore={() => void q.fetchNextPage()}
        />
      ) : null}
    </>
  );
}

function HoldersTab({ chain, address }: { chain: Chain; address: string }) {
  const t = useTranslations("token");
  const locale = useAppLocale();
  const [label, setLabel] = useState<"all" | "smart_money">("all");
  const q = useInfiniteQuery({
    queryKey: ["holders", chain, address, label],
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) =>
      apiFetch(`/tokens/${chain}/${address}/holders`, holdersSchema, {
        signal,
        query: { label, cursor: pageParam, limit: 50 },
      }),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  const first = q.data?.pages[0];
  const rows = q.data?.pages.flatMap((p) => p.items) ?? [];
  const cov = first?.coverage;
  return (
    <>
      <div className="tab-filters">
        <div className="chip-row" role="group" aria-label={t("colHolder")}>
          {(["all", "smart_money"] as const).map((l) => (
            <button
              type="button"
              key={l}
              aria-pressed={label === l}
              className={`chip ${label === l ? "selected" : ""}`}
              onClick={() => setLabel(l)}
            >
              {t(labelKey[l])}
            </button>
          ))}
        </div>
      </div>
      {cov ? (
        <p className={`coverage-note ${cov.status}`} role="note">
          {cov.status === "full"
            ? t("coverageFull", { time: formatClock(cov.as_of) })
            : cov.status === "partial"
              ? t("coveragePartial", { since: cov.since?.slice(0, 10) ?? UNKNOWN })
              : t("coverageUnknown")}
        </p>
      ) : null}
      {q.isPending ? (
        <LoadingState />
      ) : q.isError && !rows.length ? (
        <ErrorState title={t("error")} error={q.error} onRetry={() => void q.refetch()} />
      ) : !rows.length ? (
        <EmptyState title={t("smartMoneyEmpty")} />
      ) : (
        <div className="table-scroll">
          <table className="data-table">
            <thead>
              <tr>
                <th>{t("colRank")}</th>
                <th>{t("colHolder")}</th>
                <th>{t("colHolding")}</th>
                <th>{t("colShare")}</th>
                <th>{t("colAvgBuy")}</th>
                <th>{t("colAvgSell")}</th>
                <th title={t("pnlM2")}>{t("colPnl")}</th>
                <th>{t("colNative")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((h) => (
                <tr key={`${h.rank}:${h.wallet.address}`}>
                  <td className="mono">{h.rank}</td>
                  <td>
                    <Wallet chain={chain} wallet={h.wallet} />
                  </td>
                  <td className="mono">{formatQuantity(h.amount, locale)}</td>
                  <td className="mono">
                    {formatPercent(h.share_pct, { signed: false })}
                    <span className="share-bar" aria-hidden="true">
                      <span style={{ width: `${Math.min(100, Number(h.share_pct ?? 0) * 4)}%` }} />
                    </span>
                  </td>
                  <td className="mono">{formatPrice(h.avg_buy_price_usd)}</td>
                  <td className="mono">{formatPrice(h.avg_sell_price_usd)}</td>
                  <td className="mono muted" title={t("pnlM2")}>
                    {formatAmount(h.realized_pnl_usd, locale)} /{" "}
                    {formatAmount(h.unrealized_pnl_usd, locale)}
                  </td>
                  <td className="mono">
                    {h.native_balance === null
                      ? UNKNOWN
                      : `${formatQuantity(h.native_balance, locale)} ${chainMeta[chain].native}`}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {rows.length ? (
        <MoreButton
          hasMore={q.hasNextPage}
          loading={q.isFetchingNextPage}
          onMore={() => void q.fetchNextPage()}
        />
      ) : null}
    </>
  );
}

function LiquidityTab({ chain, address }: { chain: Chain; address: string }) {
  const t = useTranslations("token");
  const locale = useAppLocale();
  const [sub, setSub] = useState<"events" | "pools">("pools");
  const pools = useQuery({
    queryKey: ["pools", chain, address],
    queryFn: ({ signal }) => apiFetch(`/tokens/${chain}/${address}/pools`, poolsSchema, { signal }),
    enabled: sub === "pools",
  });
  const events = useInfiniteQuery({
    queryKey: ["liquidity-events", chain, address],
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) =>
      apiFetch(`/tokens/${chain}/${address}/liquidity-events`, liquidityEventsSchema, {
        signal,
        query: { cursor: pageParam, limit: 50 },
      }),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: sub === "events",
  });
  const eventRows = events.data?.pages.flatMap((p) => p.items) ?? [];
  const partialEvents =
    sub === "events" && (events.data?.pages.some((page) => page.coverage === "partial") ?? false);
  return (
    <>
      <div className="tab-filters">
        <div className="segmented" role="group" aria-label={t("tabLiquidity")}>
          <button
            type="button"
            aria-pressed={sub === "pools"}
            className={sub === "pools" ? "selected" : ""}
            onClick={() => setSub("pools")}
          >
            {t("liquidityPools")}
          </button>
          <button
            type="button"
            aria-pressed={sub === "events"}
            className={sub === "events" ? "selected" : ""}
            onClick={() => setSub("events")}
          >
            {t("liquidityEvents")}
          </button>
        </div>
      </div>
      {partialEvents ? (
        <p className="panel-note" role="note" title={t("liquidityEventsPartialHint")}>
          {t("liquidityEventsPartial")}
        </p>
      ) : null}
      {sub === "pools" ? (
        pools.isPending ? (
          <LoadingState />
        ) : pools.isError ? (
          <ErrorState title={t("error")} error={pools.error} onRetry={() => void pools.refetch()} />
        ) : (
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>{t("colPair")}</th>
                  <th>{t("liquidity")}</th>
                  <th>{t("colReserves")}</th>
                  <th>{t("colPoolAddress")}</th>
                  <th>{t("colCreated")}</th>
                </tr>
              </thead>
              <tbody>
                {pools.data.items.map((p) => (
                  <tr key={p.address}>
                    <td>
                      <strong>{p.dex}</strong>
                      <small className="muted"> · {p.quote_symbol ?? UNKNOWN}</small>
                      {p.is_main ? <span className="tiny-tag">{t("mainBadge")}</span> : null}
                      {p.status !== "active" ? (
                        <span className="tiny-tag muted-tag">
                          {t(p.status === "graduated" ? "graduated" : "closed")}
                        </span>
                      ) : null}
                    </td>
                    <td className="mono">{formatAmount(p.liquidity_usd, locale)}</td>
                    <td className="mono">
                      {formatQuantity(p.reserve_token, locale)}
                      <small>
                        {formatQuantity(p.reserve_quote, locale)} {p.quote_symbol ?? UNKNOWN}
                      </small>
                    </td>
                    <td>
                      <Address
                        chain={chain}
                        value={p.address}
                        showExplorer={!/^0x[0-9a-f]{64}$/i.test(p.address)}
                      />
                    </td>
                    <td className="mono" title={timeTitle(p.created_at)}>
                      {formatAge(secondsSince(p.created_at))}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      ) : events.isPending ? (
        <LoadingState />
      ) : events.isError && !eventRows.length ? (
        <ErrorState title={t("error")} error={events.error} onRetry={() => void events.refetch()} />
      ) : !eventRows.length ? (
        <EmptyState title={t("liquidityEvents")} />
      ) : (
        <>
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>{t("colKind")}</th>
                  <th>{t("colAmount")}</th>
                  <th>{t("colValue")}</th>
                  <th>{t("colWallet")}</th>
                  <th>{t("colTime")}</th>
                  <th>{t("colTx")}</th>
                </tr>
              </thead>
              <tbody>
                {eventRows.map((e) => (
                  <tr key={e.id}>
                    <td className={e.kind === "add" ? "positive" : "negative"}>{t(e.kind)}</td>
                    <td className="mono">
                      {formatQuantity(e.token_amount, locale)}
                      <small>
                        {formatQuantity(e.quote_amount, locale)} {e.quote_symbol ?? UNKNOWN}
                      </small>
                    </td>
                    <td className="mono">{formatAmount(e.value_usd, locale)}</td>
                    <td>
                      <Wallet chain={chain} wallet={e.wallet} />
                    </td>
                    <td className="mono" title={timeTitle(e.time)}>
                      {formatAge(secondsSince(e.time))}
                    </td>
                    <td>
                      <TxLink chain={chain} hash={e.tx_hash} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <MoreButton
            hasMore={events.hasNextPage}
            loading={events.isFetchingNextPage}
            onMore={() => void events.fetchNextPage()}
          />
        </>
      )}
    </>
  );
}

export function TokenTabs({ detail }: { detail: TokenDetail }) {
  const t = useTranslations("token");
  const tc = useTranslations("common");
  const locale = useAppLocale();
  const [tab, setTab] = useState<Tab>("activity");
  const { chain, address } = detail.token;
  const tabs: { id: Tab; label: string }[] = [
    { id: "activity", label: t("tabActivity") },
    { id: "holders", label: t("tabHolders", { count: formatCount(detail.holders, locale) }) },
    { id: "liquidity", label: t("tabLiquidity") },
  ];
  return (
    <section className="token-tabs market-panel">
      <div className="market-nav" role="tablist" aria-label={t("tabActivity")}>
        <div className="view-tabs">
          {tabs.map((x) => (
            <button
              type="button"
              role="tab"
              key={x.id}
              id={`tab-${x.id}`}
              aria-selected={tab === x.id}
              aria-controls={`panel-${x.id}`}
              className={tab === x.id ? "selected" : ""}
              onClick={() => setTab(x.id)}
            >
              {x.label}
            </button>
          ))}
          <button
            type="button"
            role="tab"
            aria-selected={false}
            disabled
            title={t("devTokensLocked")}
          >
            {t("tabDevTokens")}
            <span className="tiny-tag">{tc("comingSoon", { milestone: "M3" })}</span>
          </button>
        </div>
      </div>
      <div role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`}>
        {tab === "activity" ? <ActivityTab chain={chain} address={address} /> : null}
        {tab === "holders" ? <HoldersTab chain={chain} address={address} /> : null}
        {tab === "liquidity" ? <LiquidityTab chain={chain} address={address} /> : null}
      </div>
    </section>
  );
}
