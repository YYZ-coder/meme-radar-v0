"use client";
import { useTranslations } from "next-intl";
import { useEffect, useId, useRef, useState } from "react";
import { Icon } from "@/components/icon";

export type MultiOption = { id: string; label: string; detail?: string };

/** Dropdown multi-select with search, select all / clear, draft until confirmed (01-inflow §1). */
export function MultiSelect({
  label,
  allLabel,
  options,
  value,
  onChange,
}: {
  label: string;
  allLabel: string;
  options: MultiOption[];
  value: string[];
  onChange: (next: string[]) => void;
}) {
  const t = useTranslations("inflow");
  const tc = useTranslations("common");
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<string[]>(value);
  const [filter, setFilter] = useState("");
  const box = useRef<HTMLDivElement>(null);
  const panelId = useId();

  useEffect(() => {
    if (!open) return;
    const onDown = (event: MouseEvent) => {
      if (!box.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  const visible = options.filter((o) => o.label.toLowerCase().includes(filter.toLowerCase()));
  const toggleOpen = () => {
    if (!open) {
      setDraft(value);
      setFilter("");
    }
    setOpen(!open);
  };

  return (
    <div
      className="multi-select"
      ref={box}
      onKeyDown={(event) => {
        if (event.key === "Escape" && open) {
          event.stopPropagation();
          setOpen(false);
        }
      }}
    >
      <button
        type="button"
        className={`select-trigger ${value.length ? "has-filters" : ""}`}
        aria-expanded={open}
        aria-controls={panelId}
        onClick={toggleOpen}
      >
        <span>{value.length ? `${label} ${value.length}` : allLabel}</span>
        <Icon name="chevron" size={12} />
      </button>
      {open ? (
        <div className="select-panel" id={panelId} role="group" aria-label={label}>
          <input
            className="select-search"
            aria-label={`${label} ${t("facetSearch")}`}
            placeholder={t("facetSearch")}
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          />
          <div className="select-actions">
            <button type="button" onClick={() => setDraft(options.map((o) => o.id))}>
              {t("selectAll")}
            </button>
            <button type="button" onClick={() => setDraft([])}>
              {t("clear")}
            </button>
          </div>
          <div className="select-options">
            {visible.map((o) => (
              <label key={o.id} className="select-option">
                <input
                  type="checkbox"
                  checked={draft.includes(o.id)}
                  onChange={(event) =>
                    setDraft(
                      event.target.checked ? [...draft, o.id] : draft.filter((id) => id !== o.id)
                    )
                  }
                />
                <span>{o.label}</span>
                {o.detail ? <small className="mono">{o.detail}</small> : null}
              </label>
            ))}
          </div>
          <div className="select-footer">
            <span className="muted">{t("selectedCount", { count: draft.length })}</span>
            <button
              type="button"
              className="primary-button"
              onClick={() => {
                onChange(draft);
                setOpen(false);
              }}
            >
              {tc("confirm")}
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
