"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { useState, type FormEvent } from "react";
import { Icon } from "@/components/icon";
import { ErrorState } from "@/components/ui/state";
import { importReportSchema, listVersionChangeSchema } from "@/lib/api/schemas";
import type { ImportReport } from "@/lib/api/types";
import { shortAddress } from "@/lib/format/address";
import { formatClock } from "@/lib/format/time";
import { useAdmin } from "./admin-context";

const MAX_BYTES = 5 * 1024 * 1024;

export function ImportView() {
  const t = useTranslations("admin");
  const { call } = useAdmin();
  const client = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [localError, setLocalError] = useState("");
  const [report, setReport] = useState<ImportReport | null>(null);

  const upload = useMutation({
    mutationFn: (f: File) => {
      const form = new FormData();
      form.set("file", f);
      return call("/admin/smart-money/imports", importReportSchema, { method: "POST", body: form });
    },
    onSuccess: (r) => setReport(r),
  });
  const apply = useMutation({
    mutationFn: (id: string) =>
      call(`/admin/smart-money/imports/${id}/apply`, listVersionChangeSchema, { method: "POST" }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["admin"] });
      setReport((r) => (r ? { ...r, applied: true } : r));
    },
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    if (!file) return;
    if (file.size > MAX_BYTES) {
      setLocalError(t("tooLarge"));
      return;
    }
    setLocalError("");
    setReport(null);
    apply.reset();
    upload.mutate(file);
  }

  const importable = report ? report.totals.valid + report.totals.existing : 0;
  return (
    <div className="admin-import">
      <form className="import-form" onSubmit={submit}>
        <h3 className="admin-heading">{t("importTitle")}</h3>
        <p className="muted small-note">{t("importHint")}</p>
        <div className="import-row">
          <label className="file-input">
            <span className="sr-only">{t("chooseFile")}</span>
            <input
              type="file"
              accept=".csv,.json,text/csv,application/json"
              aria-label={t("chooseFile")}
              onChange={(event) => setFile(event.target.files?.[0] ?? null)}
            />
          </label>
          <button className="primary-button" type="submit" disabled={!file || upload.isPending}>
            {t("upload")}
            <Icon name="arrow" size={15} />
          </button>
        </div>
        {localError ? (
          <p className="negative" role="alert">
            {localError}
          </p>
        ) : null}
      </form>
      {upload.isError ? (
        <ErrorState title={t("error", { message: upload.error.message })} error={upload.error} />
      ) : null}
      {report ? (
        <section className="import-report" aria-label={t("report", { file: report.file_name })}>
          <div className="admin-toolbar">
            <strong>{t("report", { file: report.file_name })}</strong>
            <span className="muted small-note">
              {t("expiresAt", { time: formatClock(report.expires_at) })}
            </span>
          </div>
          <div className="chip-row report-totals">
            <span className="chip">{t("totalRows", { count: report.totals.rows })}</span>
            {(["valid", "existing", "duplicate", "invalid"] as const).map((k) => (
              <span key={k} className={`chip status-${k}`}>
                {t(`status_${k}`)} {report.totals[k]}
              </span>
            ))}
          </div>
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>{t("line")}</th>
                  <th>{t("field_chain")}</th>
                  <th>{t("field_address")}</th>
                  <th>{t("field_label")}</th>
                  <th>{t("field_tags")}</th>
                  <th>{t("field_twitter")}</th>
                  <th>{t("field_source")}</th>
                  <th>{t("field_status")}</th>
                </tr>
              </thead>
              <tbody>
                {report.rows.map((r) => (
                  <tr key={r.line} className={`status-${r.status}`}>
                    <td className="mono">{r.line}</td>
                    <td>{r.chain ?? "--"}</td>
                    <td className="mono" title={r.address ?? undefined}>
                      {r.address ? shortAddress(r.address, 6, 6) : "--"}
                    </td>
                    <td>{r.label ?? "--"}</td>
                    <td>{r.tags.join(" | ") || "--"}</td>
                    <td>{r.twitter ?? "--"}</td>
                    <td>{r.source ?? "--"}</td>
                    <td>
                      <span className={`tiny-tag status-${r.status}`}>
                        {t(`status_${r.status}`)}
                      </span>
                      {r.error ? (
                        <small className="negative">{t("errorField", { field: r.error })}</small>
                      ) : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="admin-toolbar">
            {apply.isSuccess ? (
              <p className="positive" role="status">
                {t("applied", {
                  added: apply.data.added,
                  merged: apply.data.merged,
                  version: apply.data.list_version,
                })}
              </p>
            ) : apply.isError ? (
              <p className="negative" role="alert">
                {t("error", { message: apply.error.message })}
              </p>
            ) : (
              <span />
            )}
            <button
              type="button"
              className="primary-button"
              disabled={!importable || report.applied || apply.isPending}
              onClick={() => apply.mutate(report.id)}
            >
              {importable ? t("apply", { count: importable }) : t("nothingToApply")}
            </button>
          </div>
        </section>
      ) : null}
    </div>
  );
}
