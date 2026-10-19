"use client";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { createContext, useContext, useState, type ReactNode } from "react";
import { Icon } from "@/components/icon";
import { GlobalSearch } from "@/components/shell/global-search";
import { ChainMark } from "@/components/ui/chain-mark";
import { chainMeta, chains, isChain } from "@/lib/chains";
import { useUrlParams } from "@/lib/use-url-params";
import { OverviewCards } from "./overview-cards";

export type View = "inflow" | "signals" | "leaderboard";

const RefreshContext = createContext<{ paused: boolean; setPaused: (p: boolean) => void }>({
  paused: false,
  setPaused: () => {},
});
export const useRefreshPause = () => useContext(RefreshContext);

/** Shared header for /smart-money/*: intro, overview cards, tabs and chain selector (D11 IA). */
export function SmartMoneyFrame({
  view,
  children,
  sidebar,
}: {
  view: View;
  children: ReactNode;
  sidebar?: ReactNode;
}) {
  const t = useTranslations("smartMoney");
  const [params, update] = useUrlParams();
  const [paused, setPaused] = useState(false);
  const rawChain = params.get("chain");
  const chain = isChain(rawChain) ? rawChain : null;
  const tabs: { view: View; icon: "signal" | "radar" | "grid"; label: string }[] = [
    { view: "inflow", icon: "signal", label: t("inflow") },
    { view: "signals", icon: "radar", label: t("signals") },
    { view: "leaderboard", icon: "grid", label: t("leaderboard") },
  ];
  const chainQuery = chain ? `?chain=${chain}` : "";

  function changeChain(next: string | null) {
    // Switching chain resets facets, filters and paging (00-overview §2).
    const reset: Record<string, string> = { chain: next ?? "", dex: "", platform: "", watch: "" };
    for (const key of Array.from(params.keys())) {
      if (
        key.startsWith("min_") ||
        key.startsWith("max_") ||
        key === "socials" ||
        key === "dev_status"
      )
        reset[key] = "";
    }
    update(reset);
  }

  return (
    <RefreshContext.Provider value={{ paused, setPaused }}>
      <div className="page-intro">
        <div>
          <div className="eyebrow">
            <span className="small-square" /> {t("eyebrow")}
          </div>
          <h1>
            {t("title")}
            <span className="title-cursor" />
          </h1>
          <p>{t("subtitle")}</p>
        </div>
        <div className="intro-status">
          <span className="status-dot" />
          {t("networkStatus")}
          <span className="mono" aria-hidden="true">
            ● ● ●
          </span>
        </div>
      </div>
      <OverviewCards chain={chain} paused={paused} />
      <div className={`content-grid ${sidebar ? "" : "no-sidebar"}`}>
        <section className="market-panel">
          <div className="market-nav">
            <nav className="view-tabs" aria-label={t("inflow")}>
              {tabs.map((tab) => (
                <Link
                  key={tab.view}
                  href={`/smart-money/${tab.view}${chainQuery}`}
                  className={view === tab.view ? "selected" : ""}
                  aria-current={view === tab.view ? "page" : undefined}
                >
                  <Icon name={tab.icon} size={17} />
                  {tab.label}
                </Link>
              ))}
            </nav>
          </div>
          <div className="chain-selector" role="group" aria-label={t("selectChain")}>
            <button
              type="button"
              aria-pressed={chain === null}
              className={chain === null ? "selected" : ""}
              onClick={() => changeChain(null)}
            >
              <Icon name="globe" size={14} />
              <span>{t("allChains")}</span>
              {chain === null ? <span className="chain-selected" /> : null}
            </button>
            {chains.map((c) => (
              <button
                type="button"
                key={c}
                aria-pressed={chain === c}
                className={chain === c ? "selected" : ""}
                onClick={() => changeChain(c)}
              >
                <ChainMark chain={c} />
                <span>{chainMeta[c].name}</span>
                {chain === c ? <span className="chain-selected" /> : null}
              </button>
            ))}
          </div>
          <GlobalSearch className="mobile-search" showShortcut={false} />
          {children}
        </section>
        {sidebar}
      </div>
    </RefreshContext.Provider>
  );
}
