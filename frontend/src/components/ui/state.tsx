"use client";
import { useTranslations } from "next-intl";
import type { ReactNode } from "react";
import { Icon } from "@/components/icon";
import { PixelArt } from "@/components/pixel-art";
import { useCopy } from "@/components/ui/address";
import { isApiError } from "@/lib/api/client";

export function LoadingState({ label }: { label?: string }) {
  const t = useTranslations("common");
  return (
    <div className="state-block" role="status">
      <span className="pixel-loader" aria-hidden="true">
        <i />
        <i />
        <i />
      </span>
      <span>{label ?? t("loading")}</span>
    </div>
  );
}

/** Copyable trace id so users can quote it and we can find the request in Grafana. */
export function TraceId({ id }: { id: string }) {
  const t = useTranslations("errors");
  const copy = useCopy();
  return (
    <button
      type="button"
      className="trace-id mono"
      title={t("copyTrace")}
      onClick={() => void copy(id)}
    >
      {t("traceLabel")}: {id}
      <Icon name="copy" size={10} />
    </button>
  );
}

export function ErrorState({
  title,
  detail,
  error,
  onRetry,
}: {
  title: string;
  detail?: string;
  /** API error: its code picks the message from messages.errors and its trace id is shown. */
  error?: unknown;
  onRetry?: () => void;
}) {
  const t = useTranslations("common");
  const te = useTranslations("errors");
  const api = isApiError(error) ? error : null;
  const text = detail ?? (api ? te(api.code) : error instanceof Error ? error.message : undefined);
  return (
    <div className="state-block error" role="alert">
      <Icon name="help" size={24} />
      <strong>{title}</strong>
      {text ? <p className="muted">{text}</p> : null}
      {api?.traceId ? <TraceId id={api.traceId} /> : null}
      {onRetry ? (
        <button type="button" className="outline-button" onClick={onRetry}>
          {t("retry")}
        </button>
      ) : null}
    </div>
  );
}

export function EmptyState({
  title,
  hint,
  action,
}: {
  title: string;
  hint?: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty-state">
      <Icon name="search" size={28} />
      <h3>{title}</h3>
      {hint ? <p>{hint}</p> : null}
      {action}
    </div>
  );
}

/** Feature that arrives in a later milestone. */
export function LockedState({
  title,
  hint,
  art = "alien",
  compact = false,
}: {
  title: string;
  hint: string;
  art?: string;
  compact?: boolean;
}) {
  return (
    <div className={`locked-state ${compact ? "compact" : ""}`}>
      <PixelArt art={art} color="#6f8458" size={compact ? 30 : 64} />
      <div>
        <strong>{title}</strong>
        <p>{hint}</p>
      </div>
    </div>
  );
}
