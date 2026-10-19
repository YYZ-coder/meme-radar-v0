"use client";
import { useQuery } from "@tanstack/react-query";
import dynamic from "next/dynamic";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { Icon } from "@/components/icon";
import { TokenAvatar } from "@/components/token-avatar";
import { Address, useCopy } from "@/components/ui/address";
import { ChainMark } from "@/components/ui/chain-mark";
import { EmptyState, ErrorState, LoadingState, LockedState } from "@/components/ui/state";
import { useToast } from "@/components/ui/toast";
import { apiFetch, isApiError } from "@/lib/api/client";
import { tokenDetailSchema, tokenSmartMoneySchema } from "@/lib/api/schemas";
import { summaryWindows, type Chain, type SummaryWindow, type TokenDetail } from "@/lib/api/types";
import { chainMeta, tokenKey } from "@/lib/chains";
import { shortAddress } from "@/lib/format/address";
import { formatAmount, formatCount, formatPercent, formatPrice, toneOf } from "@/lib/format/number";
import { formatAge, secondsSince, timeTitle } from "@/lib/format/time";
import { useAppLocale } from "@/lib/locale";
import { usePreferences } from "@/lib/preferences";
import { useWatchlist } from "@/lib/watchlist";
import { TokenTabs } from "./token-tabs";

// Chart library stays out of the first-load bundle (14-frontend §7).
const TokenChart = dynamic(() => import("./token-chart").then((m) => m.TokenChart), {
  ssr: false,
  loading: () => <LoadingState />,
});

function Socials({ detail }: { detail: TokenDetail }) {
  const t = useTranslations("token");
  const list = [
    { url: detail.socials.twitter, icon: "x" as const, label: t("socialTwitter") },
    { url: detail.socials.telegram, icon: "send" as const, label: t("socialTelegram") },
    { url: detail.socials.website, icon: "globe" as const, label: t("socialWebsite") },
    { url: detail.socials.github, icon: "code" as const, label: t("socialGithub") },
  ].filter((s) => s.url && /^https:\/\//.test(s.url));
  return (
    <span className="socials">
      {list.map((s) => (
        <a
          key={s.icon}
          href={s.url!}
          target="_blank"
          rel="noreferrer noopener"
          aria-label={s.label}
          title={s.label}
        >
          <Icon name={s.icon} size={13} />
        </a>
      ))}
    </span>
  );
}

function Header({ detail }: { detail: TokenDetail }) {
  const t = useTranslations("token");
  const ti = useTranslations("inflow");
  const tc = useTranslations("common");
  const locale = useAppLocale();
  const toast = useToast();
  const copy = useCopy();
  const watchlist = useWatchlist();
  const { token } = detail;
  const key = tokenKey(token);
  const starred = watchlist.has(key);
  const cur = detail.currency;
  return (
    <section className="token-header">
      <div className="token-header-main">
        <TokenAvatar chain={token.chain} address={token.address} size={56} />
        <div>
          <h1>
            {token.symbol}
            <small>{token.name}</small>
          </h1>
          <div className="token-meta">
            <span className="detail-chain">
              <ChainMark chain={token.chain} />
              {chainMeta[token.chain].name}
            </span>
            <span title={timeTitle(token.launch_at)}>
              {t("age")} {formatAge(secondsSince(token.launch_at))}
            </span>
            <Address chain={token.chain} value={token.address} kind="token" />
            <Socials detail={detail} />
            {token.launch_platform ? (
              <span className="tiny-tag">{token.launch_platform}</span>
            ) : null}
            {detail.creator ? (
              <span>
                {t("devHolding")} {formatPercent(detail.creator.holding_pct, { signed: false })}
              </span>
            ) : null}
          </div>
        </div>
        <div className="token-header-actions">
          <button
            type="button"
            className={`outline-button ${starred ? "positive" : ""}`}
            aria-pressed={starred}
            onClick={() => {
              if (!watchlist.toggle(key)) toast(ti("persistenceUnavailable"));
            }}
          >
            <Icon name="star" size={15} />
            {starred ? ti("unstar") : ti("star")}
          </button>
          <button
            type="button"
            className="outline-button"
            onClick={() => {
              void copy(window.location.href);
            }}
          >
            <Icon name="copy" size={15} />
            {tc("share")}
          </button>
        </div>
      </div>
      <dl className="token-stats">
        <div className="big">
          <dt>{t("marketCap")}</dt>
          <dd className="mono">{formatAmount(detail.market_cap, locale, cur)}</dd>
          <dd className={`mono ${toneOf(detail.price_change_24h_pct)}`}>
            {formatPercent(detail.price_change_24h_pct)} · {t("change24h")}
          </dd>
        </div>
        <div>
          <dt>{t("price")}</dt>
          <dd className="mono">{formatPrice(detail.price, cur)}</dd>
        </div>
        <div>
          <dt>{t("volume24h")}</dt>
          <dd className="mono">{formatAmount(detail.volume_24h, locale, cur)}</dd>
        </div>
        <div>
          <dt>{t("liquidity")}</dt>
          <dd className="mono">{formatAmount(detail.liquidity, locale, cur)}</dd>
        </div>
        <div>
          <dt>{t("holders")}</dt>
          <dd className="mono">
            {formatCount(detail.holders, locale)}
            {detail.holder_coverage.status === "partial" ? (
              <span className="tiny-tag muted-tag">{t("partialBadge")}</span>
            ) : null}
          </dd>
        </div>
        <div>
          <dt>{t("ath")}</dt>
          <dd
            className="mono"
            title={
              detail.ath_since ? t("athSince", { date: detail.ath_since.slice(0, 10) }) : undefined
            }
          >
            {formatAmount(detail.ath_market_cap, locale, cur)}
          </dd>
        </div>
      </dl>
    </section>
  );
}

function Summary({ detail }: { detail: TokenDetail }) {
  const t = useTranslations("token");
  const locale = useAppLocale();
  const [w, setW] = useState<SummaryWindow>("1h");
  const s = detail.windows.find((x) => x.window === w);
  const cur = detail.currency;
  return (
    <section className="side-card">
      <div className="panel-heading">
        <h2>{t("summary")}</h2>
        <div className="segmented small" role="group" aria-label={t("summary")}>
          {summaryWindows.map((x) => (
            <button
              type="button"
              key={x}
              aria-pressed={w === x}
              className={w === x ? "selected" : ""}
              onClick={() => setW(x)}
            >
              {x}
            </button>
          ))}
        </div>
      </div>
      <dl className="side-stats">
        <div>
          <dt>{t("summaryChange")}</dt>
          <dd className={`mono ${toneOf(s?.price_change_pct)}`}>
            {formatPercent(s?.price_change_pct ?? null)}
          </dd>
        </div>
        <div>
          <dt>{t("summaryVolume")}</dt>
          <dd className="mono">{formatAmount(s?.volume ?? null, locale, cur)}</dd>
        </div>
        <div>
          <dt>{t("summaryNetInflow")}</dt>
          <dd className={`mono ${toneOf(s?.net_inflow)}`}>
            {formatAmount(s?.net_inflow ?? null, locale, cur, { signed: true })}
          </dd>
        </div>
        <div>
          <dt>{t("summaryTraders")}</dt>
          <dd className="mono">{formatCount(s?.traders ?? null, locale)}</dd>
        </div>
        <div>
          <dt>{t("summaryBuys")}</dt>
          <dd className="mono positive">
            {formatCount(s?.buys ?? null, locale)} ·{" "}
            {formatAmount(s?.buy_volume ?? null, locale, cur)}
          </dd>
        </div>
        <div>
          <dt>{t("summarySells")}</dt>
          <dd className="mono negative">
            {formatCount(s?.sells ?? null, locale)} ·{" "}
            {formatAmount(s?.sell_volume ?? null, locale, cur)}
          </dd>
        </div>
      </dl>
    </section>
  );
}

function SmartMoneyPanel({ chain, address }: { chain: Chain; address: string }) {
  const t = useTranslations("token");
  const locale = useAppLocale();
  const q = useQuery({
    queryKey: ["token-sm", chain, address],
    queryFn: ({ signal }) =>
      apiFetch(`/tokens/${chain}/${address}/smart-money`, tokenSmartMoneySchema, { signal }),
    refetchInterval: 30_000,
  });
  return (
    <section className="side-card">
      <div className="panel-heading">
        <h2>
          {t("smartMoney")}
          <span className="mono muted"> {formatCount(q.data?.holders ?? null, locale)}</span>
        </h2>
      </div>
      {q.isPending ? (
        <LoadingState />
      ) : q.isError ? (
        <p className="panel-note">{q.error.message}</p>
      ) : !q.data.items.length ? (
        <p className="panel-note">{t("smartMoneyEmpty")}</p>
      ) : (
        <ul className="side-list">
          {q.data.items.slice(0, 6).map((e) => (
            <li key={e.wallet.address}>
              <Address
                chain={chain}
                value={e.wallet.address}
                label={e.wallet.display_name ?? undefined}
                showExplorer={false}
              />
              <span className="mono">{formatPercent(e.share_pct, { signed: false })}</span>
              <small className="mono">
                <span className="positive">{formatAmount(e.buy_volume_usd, locale)}</span>
                {" / "}
                <span className="negative">{formatAmount(e.sell_volume_usd, locale)}</span>
              </small>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function DeveloperPanel({ detail }: { detail: TokenDetail }) {
  const t = useTranslations("token");
  return (
    <section className="side-card">
      <div className="panel-heading">
        <h2>{t("developer")}</h2>
      </div>
      <div className="side-body">
        {detail.creator ? (
          <>
            <Address chain={detail.token.chain} value={detail.creator.address} />
            <p className="panel-note">
              {t("devHolding")} {formatPercent(detail.creator.holding_pct, { signed: false })} ·{" "}
              {t("developerHint")}
            </p>
          </>
        ) : (
          <p className="panel-note">--</p>
        )}
      </div>
    </section>
  );
}

function SameName({ detail }: { detail: TokenDetail }) {
  const t = useTranslations("token");
  const locale = useAppLocale();
  return (
    <section className="side-card">
      <div className="panel-heading">
        <h2>{t("sameName")}</h2>
        <span className="muted small-note">{t("sameNameHint")}</span>
      </div>
      {detail.same_name.length ? (
        <ul className="side-list">
          {detail.same_name.map((s) => (
            <li key={`${s.chain}:${s.address}`}>
              <Link href={`/token/${s.chain}/${s.address}`} className="same-name-link">
                <TokenAvatar chain={s.chain} address={s.address} size={22} />
                <span>
                  {s.symbol}
                  <small className="mono">
                    {chainMeta[s.chain].name} · {shortAddress(s.address)}
                  </small>
                </span>
              </Link>
              <span className="mono">{formatAmount(s.market_cap_usd, locale)}</span>
              <small className="mono">{formatAge(s.age_seconds)}</small>
            </li>
          ))}
        </ul>
      ) : (
        <p className="panel-note">{t("sameNameEmpty")}</p>
      )}
    </section>
  );
}

export function TokenView({ chain, address }: { chain: Chain; address: string }) {
  const t = useTranslations("token");
  const tc = useTranslations("common");
  const ts = useTranslations("states");
  const tch = useTranslations("chart");
  const [prefs] = usePreferences();
  const detail = useQuery({
    queryKey: ["token", chain, address, prefs.currency],
    queryFn: ({ signal }) =>
      apiFetch(`/tokens/${chain}/${address}`, tokenDetailSchema, {
        signal,
        query: { currency: prefs.currency },
      }),
    refetchInterval: 10_000,
  });

  if (detail.isPending) return <LoadingState />;
  if (detail.isError) {
    if (isApiError(detail.error, 404)) {
      return (
        <EmptyState
          title={t("notFound")}
          hint={t("notFoundHint")}
          action={
            <Link className="outline-button" href="/smart-money/inflow">
              {ts("goHome")}
            </Link>
          }
        />
      );
    }
    return (
      <ErrorState title={t("error")} error={detail.error} onRetry={() => void detail.refetch()} />
    );
  }
  const d = detail.data;
  return (
    <div className="token-page">
      <nav className="breadcrumb">
        <Link href="/smart-money/inflow">
          <Icon name="arrow" size={12} style={{ transform: "rotate(180deg)" }} />
          {tc("back")}
        </Link>
      </nav>
      <Header detail={d} />
      <div className="token-grid">
        <div className="token-main">
          <TokenChart
            chain={chain}
            address={address}
            currency={prefs.currency}
            nativeSymbol={chainMeta[chain].native}
          />
          <TokenTabs detail={d} />
        </div>
        <aside className="token-side">
          <section className="side-card">
            <LockedState
              compact
              title={`${t("tradePanel")} · ${tc("comingSoon", { milestone: "M5" })}`}
              hint={t("tradePanelLocked")}
              art="banana"
            />
          </section>
          <Summary detail={d} />
          <SmartMoneyPanel chain={chain} address={address} />
          <section className="side-card">
            <LockedState
              compact
              title={`${t("infoCard")} · ${tc("comingSoon", { milestone: "M3" })}`}
              hint={t("infoCardLocked")}
              art="cat"
            />
          </section>
          <DeveloperPanel detail={d} />
          <SameName detail={d} />
        </aside>
      </div>
      <p className="chart-attribution">
        <a href="https://www.tradingview.com/" target="_blank" rel="noreferrer noopener">
          {tch("attribution")}
        </a>
      </p>
    </div>
  );
}
