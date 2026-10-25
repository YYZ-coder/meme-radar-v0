"use client";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { memo } from "react";
import { Icon } from "@/components/icon";
import { normalizePoints, Sparkline } from "@/components/pixel-art";
import { TokenAvatar } from "@/components/token-avatar";
import type { InflowRow } from "@/lib/api/types";
import { shortAddress } from "@/lib/format/address";
import { formatAmount, formatCount, formatPercent, toneOf, type Locale } from "@/lib/format/number";
import { formatAge, timeTitle } from "@/lib/format/time";
import { useCopy } from "@/components/ui/address";

export function tokenHref(row: InflowRow) {
  return `/token/${row.token.chain}/${row.token.address}`;
}

function Socials({ socials }: { socials: InflowRow["socials"] }) {
  const t = useTranslations("token");
  const items = [
    { url: socials.twitter, icon: "x" as const, label: t("socialTwitter") },
    { url: socials.telegram, icon: "send" as const, label: t("socialTelegram") },
    { url: socials.website, icon: "globe" as const, label: t("socialWebsite") },
    { url: socials.github, icon: "code" as const, label: t("socialGithub") },
  ].filter((s) => s.url && /^https:\/\//.test(s.url));
  if (!items.length) return null;
  return (
    <span className="socials">
      {items.map((s) => (
        <a
          key={s.icon}
          href={s.url!}
          target="_blank"
          rel="noreferrer noopener"
          aria-label={s.label}
          onClick={(event) => event.stopPropagation()}
        >
          <Icon name={s.icon} size={11} />
        </a>
      ))}
    </span>
  );
}

export const InflowTableRow = memo(function InflowTableRow({
  row,
  locale,
  starred,
  hot,
  stale,
  index,
  measureRef,
  onStar,
  onOpen,
}: {
  row: InflowRow;
  locale: Locale;
  starred: boolean;
  hot: boolean;
  stale: boolean;
  index: number;
  measureRef?: (el: HTMLTableRowElement | null) => void;
  onStar: (row: InflowRow) => void;
  onOpen: (row: InflowRow) => void;
}) {
  const t = useTranslations("inflow");
  const copy = useCopy();
  const symbol = row.token.symbol.trim() || shortAddress(row.token.address);
  const cur = row.currency;
  const points = normalizePoints(row.sparkline.map((p) => (p === null ? null : Number(p))));
  return (
    <tr
      ref={measureRef}
      data-index={index}
      className={stale ? "stale-row" : undefined}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("button, a, input")) return;
        onOpen(row);
      }}
    >
      <td className="star-cell">
        <button
          type="button"
          className={`star-button ${starred ? "is-favorite" : ""}`}
          aria-label={starred ? t("unstarToken", { symbol }) : t("starToken", { symbol })}
          aria-pressed={starred}
          onClick={() => onStar(row)}
        >
          <Icon name="star" size={14} />
        </button>
      </td>
      <td>
        <div className="token-identity">
          <Link
            className="token-open"
            href={tokenHref(row)}
            aria-label={t("openToken", { symbol })}
          >
            <TokenAvatar chain={row.token.chain} address={row.token.address} />
            <span>
              <span className="token-name">
                {symbol}
                {hot ? (
                  <span className="token-hot" title={t("hot")}>
                    ✦
                  </span>
                ) : null}
              </span>
              <span className="token-subtitle">
                <span title={timeTitle(row.token.launch_at)}>{formatAge(row.age_seconds)}</span>
                <span>{shortAddress(row.token.address)}</span>
              </span>
            </span>
          </Link>
          <button
            type="button"
            className="copy-button"
            aria-label={t("copyToken", { symbol })}
            onClick={() => void copy(row.token.address)}
          >
            <Icon name="copy" size={11} />
          </button>
          <Socials socials={row.socials} />
        </div>
      </td>
      <td className="mono holder-value">
        <span className="person-pixel" />
        {formatCount(row.sm_holders, locale)}
      </td>
      <td className="mono">
        <strong>{formatCount(row.sm_trades, locale)}</strong>
      </td>
      <td className="mono">
        <span>{formatAmount(row.market_cap, locale, cur)}</span>
        <small className={toneOf(row.price_change_pct)}>
          {formatPercent(row.price_change_pct)}
        </small>
      </td>
      <td className="mono">{formatAmount(row.liquidity, locale, cur)}</td>
      <td className="mono">
        <span>{formatCount(row.trades, locale)}</span>
        <small title={t("buysSells", { buys: row.buys ?? "--", sells: row.sells ?? "--" })}>
          <span className="positive">{formatCount(row.buys, locale)}</span>
          <span className="divider-slash"> / </span>
          <span className="negative">{formatCount(row.sells, locale)}</span>
        </small>
        {row.trades === null && row.coverage === "partial" ? (
          <>
            <small className="observed-counts" title={t("parsedHint")}>
              {t("parsedCounts", {
                trades: formatCount(row.observed.trades, locale),
                buys: formatCount(row.observed.buys, locale),
                sells: formatCount(row.observed.sells, locale),
              })}
            </small>
            <small className="parsed-coverage" title={t("parsedHint")}>
              {t("parsedCoverage")}
            </small>
          </>
        ) : null}
      </td>
      <td className="mono">{formatAmount(row.volume, locale, cur)}</td>
      <td className={`mono inflow-value ${toneOf(row.net_inflow)}`}>
        {formatAmount(row.net_inflow, locale, cur, { signed: true })}
      </td>
      <td>
        <Sparkline points={points} negative={toneOf(row.price_change_pct) === "negative"} />
      </td>
    </tr>
  );
});
