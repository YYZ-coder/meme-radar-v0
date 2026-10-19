import type { Metadata } from "next";
import { Suspense } from "react";
import { InflowPage } from "@/features/inflow/inflow-view";

export const metadata: Metadata = { title: "Smart Money Inflow" };

export default function Page() {
  return (
    <Suspense fallback={null}>
      <InflowPage />
    </Suspense>
  );
}
