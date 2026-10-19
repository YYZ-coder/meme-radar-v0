"use client";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useLocale, useTranslations } from "next-intl";
import { Suspense, useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { Icon } from "@/components/icon";
import { PixelArt } from "@/components/pixel-art";
import { ChainMark } from "@/components/ui/chain-mark";
import { Dialog } from "@/components/ui/dialog";
import { isMockApi, subscribeMock } from "@/lib/api/client";
import { useNativePrices } from "@/lib/api/queries";
import { formatPercent, formatPrice, toneOf } from "@/lib/format/number";
import { localeCookie } from "@/i18n/config";
import { usePreferences } from "@/lib/preferences";
import { useWatchlist } from "@/lib/watchlist";
import { GlobalSearch } from "./global-search";

type Modal = "wallet" | "help" | "settings" | null;

function useMockFlag() {
  return useSyncExternalStore(subscribeMock, isMockApi, () => false);
}

function setLocaleCookie(next: string) {
  document.cookie = `${localeCookie}=${next}; path=/; max-age=31536000; samesite=lax`;
}

function LanguageButton() {
  const t = useTranslations("shell");
  const locale = useLocale();
  const router = useRouter();
  return (
    <button
      type="button"
      className="language-button"
      aria-label={t("language")}
      onClick={() => {
        setLocaleCookie(locale === "en" ? "zh-CN" : "en");
        router.refresh();
      }}
    >
      <Icon name="globe" size={17} />
      <span>{t("languageShort")}</span>
    </button>
  );
}

function SideRail({ onModal }: { onModal: (m: Modal) => void }) {
  const t = useTranslations("nav");
  const pathname = usePathname();
  const params = useSearchParams();
  const { keys } = useWatchlist();
  const watch = params.get("watch") === "1";
  const items = [
    {
      href: "/smart-money/inflow",
      icon: "grid" as const,
      label: t("overview"),
      active: pathname.startsWith("/smart-money") && !watch,
    },
    {
      href: "/smart-money/inflow?watch=1",
      icon: "star" as const,
      label: t("watchlist"),
      active: pathname.startsWith("/smart-money/inflow") && watch,
      badge: keys.length || null,
    },
    {
      href: "/smart-money/signals",
      icon: "signal" as const,
      label: t("signals"),
      active: false,
    },
    {
      href: "/admin",
      icon: "settings" as const,
      label: t("admin"),
      active: pathname.startsWith("/admin"),
    },
  ];
  return (
    <nav className="side-rail" aria-label={t("navigation")}>
      <div className="rail-top">
        {items.map((item) => (
          <Link
            key={item.href}
            href={item.href}
            className={`rail-button ${item.active ? "active" : ""}`}
            aria-current={item.active ? "page" : undefined}
            title={item.label}
          >
            <Icon name={item.icon} size={21} />
            <span>{item.label}</span>
            {item.badge ? <i>{item.badge}</i> : null}
          </Link>
        ))}
      </div>
      <div className="rail-bottom">
        <span className="rail-pixels" aria-hidden="true">
          ▦
        </span>
        <button
          type="button"
          className="rail-button"
          aria-label={t("help")}
          title={t("help")}
          onClick={() => onModal("help")}
        >
          <Icon name="help" size={20} />
        </button>
        <button
          type="button"
          className="rail-button"
          aria-label={t("settings")}
          title={t("settings")}
          onClick={() => onModal("settings")}
        >
          <Icon name="radar" size={20} />
        </button>
        <span className="rail-version">{t("version")}</span>
      </div>
    </nav>
  );
}

function StatusBar() {
  const t = useTranslations("shell");
  const tc = useTranslations("common");
  const natives = useNativePrices();
  const mock = useMockFlag();
  const order = ["solana", "bsc", "base"] as const;
  return (
    <footer className="statusbar">
      <div>
        <span className={natives.isError ? "status-dot error" : "status-dot"} />
        <span>{natives.isError ? t("degraded") : t("nominal")}</span>
        <span className="status-divider" />
        {order.map((chain) => {
          const item = natives.data?.items.find((n) => n.chain === chain);
          return (
            <span
              className={`ticker ${item?.stale ? "stale" : ""}`}
              key={chain}
              title={item?.stale ? tc("stale") : undefined}
            >
              <ChainMark chain={chain} />
              {item?.symbol ?? (chain === "solana" ? "SOL" : chain === "bsc" ? "BNB" : "ETH")}
              <b>{formatPrice(item?.price_usd ?? null)}</b>
              <i className={toneOf(item?.change_24h_pct)}>
                {formatPercent(item?.change_24h_pct ?? null)}
              </i>
            </span>
          );
        })}
      </div>
      <span className="statusbar-right">
        {mock ? (
          <span className="demo-flag" title={tc("mockApiHint")}>
            {tc("mockApi")}
          </span>
        ) : null}
        <span className="status-divider" />
        {tc("footer")}
      </span>
    </footer>
  );
}

function SettingsBody() {
  const t = useTranslations("shell");
  const locale = useLocale();
  const router = useRouter();
  const [prefs, update] = usePreferences();
  return (
    <>
      <label className="checkbox-label settings-row">
        <span>{t("reduceMotion")}</span>
        <input
          type="checkbox"
          checked={prefs.reduceMotion}
          onChange={(event) => update({ reduceMotion: event.target.checked })}
        />
      </label>
      <div className="settings-row">
        <span>{t("currency")}</span>
        <div className="segmented" role="group" aria-label={t("currency")}>
          {(["usd", "native"] as const).map((c) => (
            <button
              type="button"
              key={c}
              aria-pressed={prefs.currency === c}
              className={prefs.currency === c ? "selected" : ""}
              onClick={() => update({ currency: c })}
            >
              {c === "usd" ? t("currencyUsd") : t("currencyNative")}
            </button>
          ))}
        </div>
      </div>
      <div className="settings-row">
        <span>{t("displayLanguage")}</span>
        <button
          type="button"
          className="outline-button"
          onClick={() => {
            setLocaleCookie(locale === "en" ? "zh-CN" : "en");
            router.refresh();
          }}
        >
          {locale === "en" ? "简体中文" : "English"}
        </button>
      </div>
    </>
  );
}

export function AppShell({ children }: { children: ReactNode }) {
  const t = useTranslations("shell");
  const tc = useTranslations("common");
  const [modal, setModal] = useState<Modal>(null);
  const [prefs] = usePreferences();
  const searchRef = useRef<HTMLInputElement>(null);
  const mock = useMockFlag();

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (
        event.key !== "/" ||
        modal ||
        (target &&
          (target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName)))
      )
        return;
      const visible =
        searchRef.current?.offsetParent !== null
          ? searchRef.current
          : document.querySelector<HTMLInputElement>(".mobile-search input");
      if (!visible) return;
      event.preventDefault();
      visible.focus();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [modal]);

  return (
    <div className={`terminal ${prefs.reduceMotion ? "reduce-motion" : ""}`}>
      <header className="topbar">
        <Link href="/smart-money/inflow" className="brand" aria-label={tc("brand")}>
          <span className="brand-icon">
            <Icon name="radar" size={27} />
          </span>
          <span>
            {tc("brand").split(" ")[0]}
            <span className="brand-green">{tc("brand").split(" ")[1]}</span>
            <small>{tc("terminal")}</small>
          </span>
        </Link>
        <div className="topbar-divider" />
        <span className="topbar-section">
          {tc("smartMoney")}
          <span className="tiny-tag">{tc("beta")}</span>
        </span>
        <GlobalSearch className="global-search" inputRef={searchRef} />
        {mock ? (
          <span className="demo-badge" title={tc("mockApiHint")}>
            <span />
            {tc("mockApi")}
          </span>
        ) : null}
        <LanguageButton />
        <button type="button" className="connect-button" onClick={() => setModal("wallet")}>
          <Icon name="wallet" size={16} />
          <span>{t("connect")}</span>
        </button>
      </header>
      <Suspense fallback={null}>
        <SideRail onModal={setModal} />
      </Suspense>
      <main className="workspace">{children}</main>
      <StatusBar />
      {modal ? (
        <Dialog
          key={modal}
          title={
            modal === "wallet"
              ? t("walletTitle")
              : modal === "help"
                ? t("guideTitle")
                : t("settingsTitle")
          }
          onClose={() => setModal(null)}
        >
          {modal === "settings" ? (
            <SettingsBody />
          ) : (
            <>
              <div className="dialog-art">
                <PixelArt art={modal === "wallet" ? "alien" : "frog"} color="#bcf18a" size={76} />
              </div>
              <p className="dialog-description">
                {modal === "wallet" ? t("walletHint") : t("guideText")}
              </p>
              <button
                type="button"
                className="primary-button full-width"
                onClick={() => setModal(null)}
              >
                {tc("understood")}
                <Icon name="arrow" size={17} />
              </button>
            </>
          )}
        </Dialog>
      ) : null}
    </div>
  );
}
