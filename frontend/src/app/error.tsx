"use client";
// Route-level render crash fallback (18-observability §4): the shell stays usable.
import { useTranslations } from "next-intl";
import { useEffect } from "react";
import { ErrorState, TraceId } from "@/components/ui/state";

export default function RouteError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  const t = useTranslations("errors");
  useEffect(() => {
    console.error("render error", { digest: error.digest, message: error.message });
  }, [error]);
  return (
    <div className="crash-page">
      <ErrorState title={t("crashTitle")} detail={t("crashHint")} onRetry={reset} />
      {error.digest ? <TraceId id={error.digest} /> : null}
    </div>
  );
}
