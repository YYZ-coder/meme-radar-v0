"use client";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useId, useRef, useState, type Ref } from "react";
import { Icon } from "@/components/icon";
import { TokenAvatar } from "@/components/token-avatar";
import { useSearch } from "@/lib/api/queries";
import { chainMeta, explorerUrl } from "@/lib/chains";
import { shortAddress } from "@/lib/format/address";
import { formatAmount } from "@/lib/format/number";
import { formatAge } from "@/lib/format/time";
import { useAppLocale } from "@/lib/locale";
import { useDebounced } from "@/lib/use-debounced";

type Option =
  { kind: "token"; href: string; key: string } | { kind: "wallet"; href: string; key: string };

/** Global search (00-overview §2): tokens and wallets, same names are listed, never merged. */
export function GlobalSearch({
  className,
  inputRef,
  showShortcut = true,
}: {
  className: string;
  inputRef?: Ref<HTMLInputElement>;
  showShortcut?: boolean;
}) {
  const t = useTranslations("shell");
  const locale = useAppLocale();
  const router = useRouter();
  const listId = useId();
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const q = useDebounced(text, 250);
  const search = useSearch(q);
  const box = useRef<HTMLDivElement>(null);

  const tokens = search.data?.tokens ?? [];
  const wallets = search.data?.wallets ?? [];
  const options: Option[] = [
    ...tokens.map((tk) => ({
      kind: "token" as const,
      href: `/token/${tk.chain}/${tk.address}`,
      key: `t:${tk.chain}:${tk.address}`,
    })),
    ...wallets.map((w) => ({
      kind: "wallet" as const,
      href: explorerUrl(w.chain, "account", w.address),
      key: `w:${w.chain}:${w.address}`,
    })),
  ];
  const showList = open && text.trim().length > 0;

  function choose(option: Option | undefined) {
    if (!option) return;
    setOpen(false);
    setText("");
    if (option.kind === "token") router.push(option.href);
    else window.open(option.href, "_blank", "noopener,noreferrer");
  }

  return (
    <div
      ref={box}
      className={`${className} search-box`}
      onBlur={(event) => {
        if (!box.current?.contains(event.relatedTarget as Node)) setOpen(false);
      }}
    >
      <Icon name="search" size={16} />
      <input
        ref={inputRef}
        role="combobox"
        aria-expanded={showList}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={showList && options[active] ? `${listId}-${active}` : undefined}
        aria-label={t("search")}
        placeholder={t("search")}
        value={text}
        onFocus={() => setOpen(true)}
        onChange={(event) => {
          setText(event.target.value);
          setActive(0);
          setOpen(true);
        }}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown") {
            event.preventDefault();
            setActive((i) => Math.min(options.length - 1, i + 1));
          } else if (event.key === "ArrowUp") {
            event.preventDefault();
            setActive((i) => Math.max(0, i - 1));
          } else if (event.key === "Enter") {
            event.preventDefault();
            choose(options[active]);
          } else if (event.key === "Escape") {
            setOpen(false);
          }
        }}
      />
      {showShortcut ? <kbd>/</kbd> : null}
      {showList ? (
        <div className="search-results" id={listId} role="listbox">
          {search.isError ? <p className="search-note">{t("searchError")}</p> : null}
          {search.isSuccess && !options.length ? (
            <p className="search-note">{t("searchEmpty")}</p>
          ) : null}
          {tokens.length ? <p className="search-group">{t("searchTokens")}</p> : null}
          {tokens.map((tk, i) => (
            <button
              type="button"
              key={`${tk.chain}:${tk.address}`}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={active === i}
              className={active === i ? "active" : ""}
              onMouseEnter={() => setActive(i)}
              onClick={() => choose(options[i])}
            >
              <TokenAvatar chain={tk.chain} address={tk.address} size={26} />
              <span className="search-main">
                <strong>{tk.symbol}</strong>
                <small>
                  {chainMeta[tk.chain].name} · {shortAddress(tk.address)}
                </small>
              </span>
              <span className="search-side mono">
                {formatAmount(tk.market_cap_usd, locale)}
                <small>{formatAge(tk.age_seconds)}</small>
              </span>
            </button>
          ))}
          {wallets.length ? <p className="search-group">{t("searchWallets")}</p> : null}
          {wallets.map((w, j) => {
            const i = tokens.length + j;
            return (
              <button
                type="button"
                key={`${w.chain}:${w.address}`}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={active === i}
                className={active === i ? "active" : ""}
                onMouseEnter={() => setActive(i)}
                onClick={() => choose(options[i])}
              >
                <TokenAvatar chain={w.chain} address={w.address} size={26} />
                <span className="search-main">
                  <strong>{w.label ?? shortAddress(w.address)}</strong>
                  <small>
                    {chainMeta[w.chain].name} · {shortAddress(w.address)}
                  </small>
                </span>
                {w.is_smart_money ? <span className="tiny-tag">{t("smartMoneyTag")}</span> : null}
                <Icon name="external" size={12} />
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}
