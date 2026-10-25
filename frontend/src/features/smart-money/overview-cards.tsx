"use client";
import { useQuery } from "@tanstack/react-query";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { Icon } from "@/components/icon";
import { PixelArt } from "@/components/pixel-art";
import { ChainMark } from "@/components/ui/chain-mark";
import { apiFetch } from "@/lib/api/client";
import { overviewSchema } from "@/lib/api/schemas";
import { windows, type Chain, type Window } from "@/lib/api/types";
import { chainMeta } from "@/lib/chains";
import { formatAmount, formatCount, formatPercent, toneOf, UNKNOWN } from "@/lib/format/number";
import { useAppLocale } from "@/lib/locale";

export function OverviewCards({ chain, paused }: { chain: Chain | null; paused: boolean }) {
  const t = useTranslations("overview");
  const locale = useAppLocale();
  const params = useSearchParams();
  const raw = params.get("window");
  const window: Window = (windows as readonly string[]).includes(raw ?? "")
    ? (raw as Window)
    : "1h";
  const overview = useQuery({
    queryKey: ["overview", chain, window],
    queryFn: ({ signal }) =>
      apiFetch("/smart-money/overview", overviewSchema, {
        signal,
        query: { chain, window },
      }),
    refetchInterval: paused ? false : 10_000,
  });
  const d = overview.data;
  const bars = (d?.inflow_bars ?? []).map((b) => (b === null ? 0 : Math.abs(Number(b))));
  const maxBar = Math.max(1, ...bars);
  const buyShare =
    d?.buy_share_pct === null || d?.buy_share_pct === undefined ? null : Number(d.buy_share_pct);
  const buyBlocks = buyShare === null ? 0 : Math.round((buyShare / 100) * 24);
  const splitAmountsKnown =
    d?.coverage === "complete" &&
    d.large_buy_volume_usd !== null &&
    d.small_buy_volume_usd !== null;
  const splitReady =
    splitAmountsKnown && d.large_buy_share_pct !== null && d.small_buy_share_pct !== null;
  const noBuys =
    splitAmountsKnown &&
    Number(d.large_buy_volume_usd) === 0 &&
    Number(d.small_buy_volume_usd) === 0 &&
    d.large_buy_share_pct === null &&
    d.small_buy_share_pct === null;
  const largeShare = splitReady ? d.large_buy_share_pct : null;
  const smallShare = splitReady ? d.small_buy_share_pct : null;

  return (
    <>
      {d?.coverage === "partial" ? (
        <p className="panel-note" role="note" title={t("partialHint")}>
          {t("partialCoverage")}
        </p>
      ) : null}
      <section
        className="summary-grid"
        aria-label={t("marketPulse")}
        aria-busy={overview.isPending}
      >
        <article className="summary-card primary-summary">
          <div className="card-caption">
            <span>{t("netInflow", { window })}</span>
            <Icon name="arrow" size={16} />
          </div>
          <strong className={toneOf(d?.net_inflow_usd)}>
            {formatAmount(d?.net_inflow_usd ?? null, locale, "USD", { signed: true })}
          </strong>
          <div className="metric-foot">
            <span className={toneOf(d?.net_inflow_change_pct)}>
              {formatPercent(d?.net_inflow_change_pct ?? null)}
            </span>
            <span>{t("vsPrevious")}</span>
          </div>
          <div className="metric-bars" aria-hidden="true">
            {bars.map((b, i) => (
              <span key={i} style={{ height: `${Math.max(6, (b / maxBar) * 100)}%` }} />
            ))}
          </div>
          <span className="card-corner">01</span>
        </article>
        <article className="summary-card">
          <div className="card-caption">
            <span>{t("activeWallets", { window })}</span>
            <Icon name="wallet" size={16} />
          </div>
          <strong>
            {formatCount(d?.active_wallets ?? null, locale)}
            <span className="metric-unit">{t("walletsUnit")}</span>
          </strong>
          <div className="metric-foot">
            <span className="avatar-stack" aria-hidden="true">
              <PixelArt art="alien" color="#a4b5f6" size={24} />
              <PixelArt art="frog" size={24} />
              <PixelArt art="boy" color="#c5a5d9" size={24} />
              <PixelArt art="cat" color="#ddbd91" size={24} />
            </span>
            <span>{t("walletsTrading")}</span>
          </div>
          <span className="card-corner">02</span>
        </article>
        <article className="summary-card">
          <div className="card-caption">
            <span>{t("trackedTokens")}</span>
            <Icon name="grid" size={16} />
          </div>
          <strong>
            {formatCount(d?.tracked_tokens ?? null, locale)}
            <span className="metric-unit">{t("tokensUnit")}</span>
          </strong>
          <div className="metric-foot">
            <span className="chain-stack" aria-hidden="true">
              <ChainMark chain="solana" />
              <ChainMark chain="base" />
              <ChainMark chain="bsc" />
            </span>
            <span>{chain ? chainMeta[chain].name : t("acrossChains")}</span>
            {d?.new_tokens ? (
              <span className="positive mini-metric">
                {t("newTokens", { count: d.new_tokens })}
              </span>
            ) : null}
          </div>
          <span className="card-corner">03</span>
        </article>
        <article className="summary-card pulse-card">
          <div className="card-caption">
            <span>{t("marketPulse")}</span>
            <span className="eyebrow">
              {buyShare === null
                ? UNKNOWN
                : buyShare >= 50
                  ? `${t("bullish")} ↗`
                  : `${t("bearish")} ↘`}
            </span>
          </div>
          <strong className="pulse-label">
            {t("buyPressure")}
            <span>
              {buyShare === null ? UNKNOWN : Math.round(buyShare)}
              <span className="percentage">%</span>
            </span>
          </strong>
          <div className="pixel-pressure" aria-hidden="true">
            {Array.from({ length: 24 }, (_, i) => (
              <span key={i} className={buyShare === null ? "" : i < buyBlocks ? "buy" : "sell"} />
            ))}
          </div>
          <div className="pressure-labels">
            <span>
              {t("buy")} {formatAmount(d?.buy_volume_usd ?? null, locale)}
            </span>
            <span>
              {t("sell")} {formatAmount(d?.sell_volume_usd ?? null, locale)}
            </span>
          </div>
          <div
            className="position-buy-split"
            role="group"
            aria-label={t("positionBuySplit")}
            title={t("positionBuyHint")}
          >
            <div className="position-buy-caption">
              <span>{t("positionBuySplit")}</span>
              <span>{t("positionThreshold")}</span>
            </div>
            {!splitReady ? (
              <p className="position-buy-status">
                {t(noBuys ? "positionNoBuys" : "positionPending")}
              </p>
            ) : null}
            <dl className="position-buy-labels">
              <div className="large-position">
                <dt>{t("largePositionBuy")}</dt>
                <dd>
                  <span>{formatPercent(largeShare, { signed: false })}</span>
                  <small>
                    {formatAmount(splitAmountsKnown ? d.large_buy_volume_usd : null, locale)}
                  </small>
                </dd>
              </div>
              <div className="small-position">
                <dt>{t("smallPositionBuy")}</dt>
                <dd>
                  <span>{formatPercent(smallShare, { signed: false })}</span>
                  <small>
                    {formatAmount(splitAmountsKnown ? d.small_buy_volume_usd : null, locale)}
                  </small>
                </dd>
              </div>
            </dl>
            <div
              className={`position-buy-bar${splitReady ? "" : " pending"}`}
              role={splitReady ? "img" : undefined}
              aria-label={
                splitReady
                  ? t("positionBarLabel", {
                      large: formatPercent(largeShare, { signed: false }),
                      small: formatPercent(smallShare, { signed: false }),
                    })
                  : undefined
              }
              aria-hidden={splitReady ? undefined : true}
            >
              {splitReady ? (
                <>
                  <span style={{ width: `${Number(largeShare)}%` }} />
                  <span style={{ width: `${Number(smallShare)}%` }} />
                </>
              ) : null}
            </div>
          </div>
        </article>
      </section>
    </>
  );
}
