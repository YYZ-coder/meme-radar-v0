"use client";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { Icon } from "@/components/icon";
import { Radar } from "@/components/pixel-art";
import { TokenAvatar } from "@/components/token-avatar";
import { LockedState } from "@/components/ui/state";
import type { InflowRow } from "@/lib/api/types";
import { formatAmount } from "@/lib/format/number";
import { useAppLocale } from "@/lib/locale";
import { usePreferences } from "@/lib/preferences";
import { tokenHref } from "./inflow-row";

export function InflowSidebar({ rows, paused }: { rows: InflowRow[]; paused: boolean }) {
  const t = useTranslations("sidebar");
  const tc = useTranslations("common");
  const ts = useTranslations("smartMoney");
  const locale = useAppLocale();
  const [prefs] = usePreferences();
  // Display-only ranking of rows already loaded; it never reorders the list itself.
  const top = rows
    .filter((r) => r.net_inflow !== null && Number(r.net_inflow) > 0)
    .sort((a, b) => Number(b.net_inflow) - Number(a.net_inflow))
    .slice(0, 3);
  return (
    <aside className="insights-sidebar">
      <section className="radar-panel">
        <div className="panel-caption">
          <span>
            <span className="status-dot" />
            {t("scan")}
          </span>
          <span className="mono">{t("on")}</span>
        </div>
        <Radar quiet={prefs.reduceMotion || paused} tag={t("alphaFound")} />
        <div className="radar-bottom">
          <span className="mono">{t("chains")}</span>
          <span className="positive">
            {t("detected")}
            <span className="blink">_</span>
          </span>
        </div>
      </section>
      <section className="signals-panel">
        <div className="panel-heading">
          <h2>
            <Icon name="signal" size={15} />
            {t("topInflow")}
          </h2>
        </div>
        {top.length ? (
          top.map((row) => (
            <Link
              className="feed-signal"
              key={`${row.token.chain}:${row.token.address}`}
              href={tokenHref(row)}
            >
              <div className="feed-header">
                <TokenAvatar
                  chain={row.token.chain}
                  address={row.token.address}
                  size={28}
                  withChain={false}
                />
                <strong>{row.token.symbol}</strong>
                <span>{formatAmount(row.market_cap, locale, row.currency)}</span>
              </div>
              <div className="feed-amount">
                <span className="buy-tag">↗ {tc("smartMoney")}</span>
                <strong className="positive">
                  {formatAmount(row.net_inflow, locale, row.currency, { signed: true })}
                </strong>
              </div>
            </Link>
          ))
        ) : (
          <p className="panel-note">{t("noTop")}</p>
        )}
        <p className="panel-note">{t("topInflowHint")}</p>
      </section>
      <section className="wallet-panel">
        <LockedState
          compact
          title={`${t("signalsTeaser")} · ${tc("comingSoon", { milestone: "M3" })}`}
          hint={ts("signalsLocked")}
          art="frog"
        />
      </section>
      <section className="wallet-panel">
        <LockedState
          compact
          title={`${t("walletTeaser")} · ${tc("comingSoon", { milestone: "M2" })}`}
          hint={ts("leaderboardLocked")}
          art="alien"
        />
      </section>
    </aside>
  );
}
