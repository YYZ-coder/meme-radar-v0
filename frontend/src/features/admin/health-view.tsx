"use client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { ChainMark } from "@/components/ui/chain-mark";
import { ErrorState, LoadingState } from "@/components/ui/state";
import { parseFailureSchema, adminHealthSchema } from "@/lib/api/schemas";
import type { ChainHealth } from "@/lib/api/types";
import { chainMeta } from "@/lib/chains";
import { formatAmount, formatCount } from "@/lib/format/number";
import { formatClock } from "@/lib/format/time";
import { useAppLocale } from "@/lib/locale";
import { useAdmin } from "./admin-context";

function Progress({ value, label }: { value: number | null; label: string }) {
  const pct = value === null ? 0 : Math.max(0, Math.min(100, value));
  return (
    <div
      className="progress"
      role="progressbar"
      aria-label={label}
      aria-valuenow={Math.round(pct)}
      aria-valuemin={0}
      aria-valuemax={100}
    >
      <span style={{ width: `${pct}%` }} />
    </div>
  );
}

function ChainCard({ c }: { c: ChainHealth }) {
  const t = useTranslations("admin");
  const locale = useAppLocale();
  const historyPct =
    c.history_position === null
      ? null
      : ((c.anchor - c.history_position) / Math.max(1, c.anchor - c.lower_bound)) * 100;
  const gaps = Object.entries(c.gaps_by_reason);
  return (
    <article className="side-card health-card">
      <div className="panel-heading">
        <h2>
          <ChainMark chain={c.chain} />
          {chainMeta[c.chain].name}
        </h2>
        {c.budget_paused ? <span className="tiny-tag negative">{t("budgetPaused")}</span> : null}
      </div>
      <dl className="side-stats">
        <div>
          <dt>{t("head")}</dt>
          <dd className="mono">{formatCount(c.head, locale)}</dd>
        </div>
        <div>
          <dt>{t("forward")}</dt>
          <dd className="mono">{formatCount(c.forward_position, locale)}</dd>
        </div>
        <div>
          <dt>{t("backlog")}</dt>
          <dd className="mono">
            {c.backlog_blocks === null ? "--" : t("blocks", { count: c.backlog_blocks })}
          </dd>
        </div>
        <div>
          <dt>{t("lag")}</dt>
          <dd className="mono">
            {c.lag_seconds === null ? "--" : t("seconds", { count: c.lag_seconds })}
          </dd>
        </div>
        <div>
          <dt>{t("anchor")}</dt>
          <dd className="mono">{formatCount(c.anchor, locale)}</dd>
        </div>
        <div>
          <dt>{t("lowerBound")}</dt>
          <dd className="mono">{formatCount(c.lower_bound, locale)}</dd>
        </div>
      </dl>
      <div className="side-body">
        <span className="muted small-note">
          {t("history")} · {formatCount(c.history_position, locale)}
        </span>
        <Progress value={historyPct} label={t("history")} />
        <p className="small-note">
          {t("gaps")}:{" "}
          {gaps.length ? (
            <span className={c.gaps_open ? "negative" : ""}>
              {c.gaps_open} ({gaps.map(([k, v]) => `${k} ${v}`).join(", ")})
            </span>
          ) : (
            <span className="positive">{t("noGaps")}</span>
          )}
        </p>
        <p className="small-note">
          {t("lastError")}:{" "}
          {c.last_error ? (
            <span className="negative" title={c.last_error.at}>
              [{c.last_error.kind}] {c.last_error.message}
            </span>
          ) : (
            t("noError")
          )}
        </p>
      </div>
    </article>
  );
}

export function HealthView() {
  const t = useTranslations("admin");
  const locale = useAppLocale();
  const { call } = useAdmin();
  const client = useQueryClient();
  const health = useQuery({
    queryKey: ["admin", "health"],
    queryFn: ({ signal }) => call("/admin/health", adminHealthSchema, { signal }),
    refetchInterval: 15_000,
  });
  const retry = useMutation({
    mutationFn: (id: string) =>
      call(`/admin/parse-failures/${id}/retry`, parseFailureSchema, { method: "POST" }),
    onSuccess: () => client.invalidateQueries({ queryKey: ["admin", "health"] }),
  });
  if (health.isPending) return <LoadingState />;
  if (health.isError)
    return (
      <ErrorState
        title={t("error", { message: health.error.message })}
        error={health.error}
        onRetry={() => void health.refetch()}
      />
    );
  const h = health.data;
  const spent = Number(h.budget.spent_usd);
  const limit = Number(h.budget.limit_usd) || 1;
  return (
    <div className="admin-health">
      <div className="admin-toolbar">
        <span className="muted">{t("asOf", { time: formatClock(h.as_of) })}</span>
        <button
          type="button"
          className="outline-button bordered"
          onClick={() => void health.refetch()}
        >
          {t("refresh")}
        </button>
      </div>
      <h3 className="admin-heading">{t("chainProgress")}</h3>
      <div className="health-grid">
        {h.chains.map((c) => (
          <ChainCard key={c.chain} c={c} />
        ))}
      </div>
      <h3 className="admin-heading">{t("budget")}</h3>
      <div className="side-card side-body budget-card">
        <Progress value={(spent / limit) * 100} label={t("budget")} />
        <div className="budget-marks" aria-hidden="true">
          <span style={{ left: "70%" }}>70%</span>
          <span style={{ left: "80%" }}>80%</span>
        </div>
        <p className="small-note">
          {t(`budgetState_${h.budget.state}`)} ·{" "}
          {t("budgetSpent", {
            spent: formatAmount(h.budget.spent_usd, locale),
            limit: formatAmount(h.budget.limit_usd, locale),
            month: h.budget.month,
          })}
        </p>
      </div>
      <h3 className="admin-heading">{t("parsers")}</h3>
      <div className="table-scroll">
        <table className="data-table">
          <thead>
            <tr>
              <th>{t("module")}</th>
              <th>{t("version")}</th>
              <th>{t("position")}</th>
              <th>{t("lag")}</th>
              <th>{t("quarantined")}</th>
              <th>{t("failures")}</th>
              <th>{t("unknownDisc")}</th>
            </tr>
          </thead>
          <tbody>
            {h.parsers.map((p) => (
              <tr key={`${p.chain}:${p.module}`}>
                <td>
                  <ChainMark chain={p.chain} /> {p.module}
                </td>
                <td className="mono">{p.version}</td>
                <td className="mono">{formatCount(p.position, locale)}</td>
                <td className="mono">
                  {p.lag_blocks === null ? "--" : t("blocks", { count: p.lag_blocks })}
                </td>
                <td className={`mono ${p.quarantined ? "negative" : ""}`}>{p.quarantined}</td>
                <td className={`mono ${p.failures ? "negative" : ""}`}>{p.failures}</td>
                <td className="mono">{p.unknown_discriminators}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <h3 className="admin-heading">{t("failedTasks")}</h3>
      {h.failures.length ? (
        <div className="table-scroll">
          <table className="data-table">
            <tbody>
              {h.failures.map((f) => (
                <tr key={f.id}>
                  <td>
                    <ChainMark chain={f.chain} /> {f.module}
                  </td>
                  <td className="mono">{f.position}</td>
                  <td className="negative">{f.error}</td>
                  <td className="muted">{t("attempts", { count: f.attempts })}</td>
                  <td>
                    {f.status === "queued" ? (
                      <span className="tiny-tag">{t("queued")}</span>
                    ) : (
                      <button
                        type="button"
                        className="outline-button bordered"
                        disabled={retry.isPending}
                        onClick={() => retry.mutate(f.id)}
                      >
                        {t("retry")}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="panel-note">{t("noFailures")}</p>
      )}
    </div>
  );
}
