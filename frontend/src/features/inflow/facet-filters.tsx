"use client";

import { useTranslations } from "next-intl";
import { MultiSelect } from "@/components/ui/multi-select";
import type { FacetsResponse } from "@/lib/api/types";
import { formatAmount, formatCount, type Locale } from "@/lib/format/number";

type FacetFiltersProps = {
  facets?: FacetsResponse;
  locale: Locale;
  dex: string[];
  platform: string[];
  onDexChange: (next: string[]) => void;
  onPlatformChange: (next: string[]) => void;
};

export function FacetFilters({
  facets,
  locale,
  dex,
  platform,
  onDexChange,
  onPlatformChange,
}: FacetFiltersProps) {
  const t = useTranslations("inflow");
  const partial = facets?.coverage === "partial";
  const parsed = (count: number) =>
    `-- · ${t("facetParsedCount", { count: formatCount(count, locale) })}`;
  return (
    <>
      <MultiSelect
        label={t("dex")}
        allLabel={t("dexAll")}
        value={dex}
        options={(facets?.dexes ?? []).map((facet) => ({
          id: facet.id,
          label: facet.name,
          detail: partial
            ? parsed(facet.observed_token_count)
            : formatAmount(facet.volume_usd, locale),
        }))}
        onChange={onDexChange}
      />
      <MultiSelect
        label={t("platform")}
        allLabel={t("platformAll")}
        value={platform}
        options={(facets?.platforms ?? []).map((facet) => ({
          id: facet.id,
          label: facet.name,
          detail: partial
            ? parsed(facet.observed_token_count)
            : formatCount(facet.token_count, locale),
        }))}
        onChange={onPlatformChange}
      />
      {partial ? (
        <span className="muted" role="note" title={t("facetParsedHint")}>
          {t("facetCoverage")}
        </span>
      ) : null}
    </>
  );
}
