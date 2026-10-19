import type { Metadata } from "next";
import { Suspense } from "react";
import { LockedView } from "@/features/smart-money/locked-view";

export const metadata: Metadata = { title: "Leaderboard" };

export default function Page() {
  return (
    <Suspense fallback={null}>
      <LockedView view="leaderboard" />
    </Suspense>
  );
}
