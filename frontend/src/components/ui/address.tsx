"use client";
import { useTranslations } from "next-intl";
import { Icon } from "@/components/icon";
import { useToast } from "@/components/ui/toast";
import type { Chain } from "@/lib/api/types";
import { explorerUrl } from "@/lib/chains";
import { shortAddress } from "@/lib/format/address";

export function useCopy() {
  const t = useTranslations("common");
  const toast = useToast();
  return async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast(t("copied"));
    } catch {
      toast(t("copyFailed"));
    }
  };
}

/** Short address with copy button and explorer link (00-overview §3). */
export function Address({
  chain,
  value,
  kind = "account",
  label,
  showExplorer = true,
}: {
  chain: Chain;
  value: string;
  kind?: "token" | "account" | "tx";
  label?: string;
  showExplorer?: boolean;
}) {
  const t = useTranslations("common");
  const copy = useCopy();
  return (
    <span className="address" title={value}>
      <span className="mono">{label ?? shortAddress(value)}</span>
      <button
        type="button"
        className="copy-button"
        aria-label={`${t("copy")} ${shortAddress(value)}`}
        onClick={(event) => {
          event.stopPropagation();
          void copy(value);
        }}
      >
        <Icon name="copy" size={11} />
      </button>
      {showExplorer ? (
        <a
          className="copy-button"
          href={explorerUrl(chain, kind, value)}
          target="_blank"
          rel="noreferrer noopener"
          aria-label={t("explorer")}
          onClick={(event) => event.stopPropagation()}
        >
          <Icon name="external" size={11} />
        </a>
      ) : null}
    </span>
  );
}
