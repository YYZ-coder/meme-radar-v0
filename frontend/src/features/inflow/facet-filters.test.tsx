import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { NextIntlClientProvider } from "next-intl";
import { describe, expect, it, vi } from "vitest";
import type { FacetsResponse } from "@/lib/api/types";
import en from "@/messages/en.json";
import zh from "@/messages/zh-CN.json";
import { FacetFilters } from "./facet-filters";

const facet = {
  id: "uniswap_v4",
  name: "Uniswap v4",
  chain: "base" as const,
  token_count: null,
  observed_token_count: 2,
  volume_usd: null,
};
const partial: FacetsResponse = {
  coverage: "partial",
  dexes: [facet],
  platforms: [{ ...facet, id: "clanker", name: "Clanker" }],
};

function show(facets: FacetsResponse, locale: "en" | "zh-CN" = "en") {
  render(
    <NextIntlClientProvider locale={locale} messages={locale === "en" ? en : zh}>
      <FacetFilters
        facets={facets}
        locale={locale}
        dex={[]}
        platform={[]}
        onDexChange={vi.fn()}
        onPlatformChange={vi.fn()}
      />
    </NextIntlClientProvider>
  );
  return userEvent.setup();
}

describe("facet coverage labels", () => {
  it.each([
    [
      "en",
      "Filter counts · incomplete coverage",
      "Parsed 2",
      "All DEX",
      "All launchpads",
      "DEX",
      "Launchpad",
    ],
    ["zh-CN", "筛选计数·覆盖不完整", "已解析 2", "全部 DEX", "全部平台", "DEX", "发射平台"],
  ] as const)(
    "labels parsed token counts in %s without inventing totals",
    async (locale, hint, count, dex, platform, dexGroup, platformGroup) => {
      const user = show(partial, locale);
      expect(screen.getByRole("note")).toHaveTextContent(hint);
      await user.click(screen.getByRole("button", { name: dex }));
      expect(
        within(screen.getByRole("group", { name: dexGroup })).getByText(`-- · ${count}`)
      ).toBeVisible();
      await user.click(screen.getByRole("button", { name: platform }));
      expect(
        within(screen.getByRole("group", { name: platformGroup })).getByText(`-- · ${count}`)
      ).toBeVisible();
    }
  );

  it("retains a parsed zero as an incomplete observation", async () => {
    const user = show({
      ...partial,
      platforms: [{ ...partial.platforms[0], observed_token_count: 0 }],
    });
    await user.click(screen.getByRole("button", { name: "All launchpads" }));
    expect(screen.getByText("-- · Parsed 0")).toBeVisible();
    expect(screen.getByRole("note")).toBeVisible();
  });

  it("uses only the complete count when full coverage is available", async () => {
    const user = show({
      ...partial,
      coverage: "complete",
      platforms: [{ ...partial.platforms[0], token_count: 3 }],
    });
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "All launchpads" }));
    expect(screen.getByText("3")).toBeVisible();
    expect(screen.queryByText(/Parsed/)).not.toBeInTheDocument();
  });

  it("keeps an empty partial response usable", () => {
    show({ coverage: "partial", dexes: [], platforms: [] });
    expect(screen.getByRole("note")).toBeVisible();
    expect(screen.getByRole("button", { name: "All launchpads" })).toBeEnabled();
  });
});
