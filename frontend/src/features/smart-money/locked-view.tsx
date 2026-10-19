"use client";
import { useTranslations } from "next-intl";
import { LockedState } from "@/components/ui/state";
import { SmartMoneyFrame, type View } from "./frame";

export function LockedView({ view }: { view: Exclude<View, "inflow"> }) {
  const t = useTranslations("smartMoney");
  const tc = useTranslations("common");
  const signals = view === "signals";
  return (
    <SmartMoneyFrame view={view}>
      <div className="locked-panel">
        <LockedState
          title={t("lockedTitle", { feature: signals ? t("signals") : t("leaderboard") })}
          hint={signals ? t("signalsLocked") : t("leaderboardLocked")}
          art={signals ? "frog" : "alien"}
        />
        <span className="tiny-tag">{tc("comingSoon", { milestone: signals ? "M3" : "M2" })}</span>
      </div>
    </SmartMoneyFrame>
  );
}
