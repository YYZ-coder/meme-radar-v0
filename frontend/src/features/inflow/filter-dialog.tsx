"use client";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { Icon } from "@/components/icon";
import { Dialog } from "@/components/ui/dialog";
import {
  emptyFilters,
  integerFields,
  invalidRanges,
  rangeFields,
  socialKeys,
  validNumber,
  type InflowFilters,
  type RangeField,
} from "./url-state";

const labels: Record<RangeField, string> = {
  market_cap: "marketCap",
  price_change: "priceChange",
  liquidity: "liquidity",
  trades: "trades",
  volume: "volume",
  net_inflow: "netInflow",
  sm_holders: "smHolders",
  sm_trades: "smTrades",
  holders: "holders",
  age: "age",
};

const ageUnits = { minutes: 60, hours: 3600, days: 86400 } as const;
type AgeUnit = keyof typeof ageUnits;

function pickUnit(min: string, max: string): AgeUnit {
  const values = [min, max].filter(Boolean).map(Number);
  if (values.length && values.every((v) => v % 86400 === 0)) return "days";
  if (values.length && values.every((v) => v % 3600 === 0)) return "hours";
  return "minutes";
}

/** Filter drawer (01-inflow §3): draft vs applied; confirm applies and returns to page one. */
export function FilterDialog({
  filters,
  onApply,
  onClose,
}: {
  filters: InflowFilters;
  onApply: (next: InflowFilters) => void;
  onClose: () => void;
}) {
  const t = useTranslations("filters");
  const [draft, setDraft] = useState<InflowFilters>(filters);
  const initialUnit = pickUnit(filters.ranges.age.min, filters.ranges.age.max);
  const [ageUnit, setAgeUnit] = useState<AgeUnit>(initialUnit);
  const toUnit = (seconds: string) =>
    seconds === "" ? "" : String(Number(seconds) / ageUnits[initialUnit]);
  const [ageText, setAgeText] = useState({
    min: toUnit(filters.ranges.age.min),
    max: toUnit(filters.ranges.age.max),
  });

  const ageSeconds = (text: string, unit: AgeUnit) =>
    /^\d+(\.\d+)?$/.test(text) ? String(Math.round(Number(text) * ageUnits[unit])) : "";
  const effective: InflowFilters = {
    ...draft,
    ranges: {
      ...draft.ranges,
      age: { min: ageSeconds(ageText.min, ageUnit), max: ageSeconds(ageText.max, ageUnit) },
    },
  };
  const invalid = new Set(invalidRanges(effective));

  function setRange(field: RangeField, side: "min" | "max", value: string) {
    setDraft({
      ...draft,
      ranges: { ...draft.ranges, [field]: { ...draft.ranges[field], [side]: value } },
    });
  }

  return (
    <Dialog title={t("title")} onClose={onClose} wide>
      <form
        className="filter-form"
        onSubmit={(event) => {
          event.preventDefault();
          if (invalid.size) return;
          const clean = { ...effective, ranges: { ...effective.ranges } };
          for (const field of rangeFields) {
            clean.ranges[field] = {
              min: validNumber(field, clean.ranges[field].min),
              max: validNumber(field, clean.ranges[field].max),
            };
          }
          onApply(clean);
        }}
      >
        <p className="muted dialog-description">{t("description")}</p>
        <div className="range-grid">
          {rangeFields.map((field) => {
            const isAge = field === "age";
            const step = integerFields.has(field) && !isAge ? "1" : "any";
            const min = isAge ? ageText.min : draft.ranges[field].min;
            const max = isAge ? ageText.max : draft.ranges[field].max;
            const signed = field === "price_change" || field === "net_inflow";
            return (
              <fieldset
                key={field}
                className={`range-field ${invalid.has(field) ? "invalid" : ""}`}
              >
                <legend>{t(labels[field])}</legend>
                <input
                  type="number"
                  inputMode="decimal"
                  step={step}
                  min={signed ? undefined : 0}
                  aria-label={`${t(labels[field])} ${t("min")}`}
                  placeholder={t("min")}
                  value={min}
                  onChange={(event) =>
                    isAge
                      ? setAgeText({ ...ageText, min: event.target.value })
                      : setRange(field, "min", event.target.value)
                  }
                />
                <span aria-hidden="true">~</span>
                <input
                  type="number"
                  inputMode="decimal"
                  step={step}
                  min={signed ? undefined : 0}
                  aria-label={`${t(labels[field])} ${t("max")}`}
                  placeholder={t("max")}
                  value={max}
                  onChange={(event) =>
                    isAge
                      ? setAgeText({ ...ageText, max: event.target.value })
                      : setRange(field, "max", event.target.value)
                  }
                />
                {isAge ? (
                  <select
                    aria-label={t("ageUnit")}
                    value={ageUnit}
                    onChange={(event) => setAgeUnit(event.target.value as AgeUnit)}
                  >
                    {(Object.keys(ageUnits) as AgeUnit[]).map((unit) => (
                      <option key={unit} value={unit}>
                        {t(unit)}
                      </option>
                    ))}
                  </select>
                ) : null}
                {invalid.has(field) ? <small className="negative">{t("invalid")}</small> : null}
              </fieldset>
            );
          })}
        </div>
        <fieldset className="toggle-field">
          <legend>{t("devStatus")}</legend>
          {(["", "sold", "holding"] as const).map((value) => (
            <label key={value || "any"} className="checkbox-label">
              <input
                type="radio"
                name="dev_status"
                checked={draft.devStatus === value}
                onChange={() => setDraft({ ...draft, devStatus: value })}
              />
              {t(value === "" ? "devAny" : value === "sold" ? "devSold" : "devHolding")}
            </label>
          ))}
          <small className="muted">{t("m3Note")}</small>
        </fieldset>
        <fieldset className="toggle-field">
          <legend>{t("socials")}</legend>
          {socialKeys.map((key) => (
            <label key={key} className="checkbox-label">
              <input
                type="checkbox"
                checked={draft.socials.includes(key)}
                onChange={(event) =>
                  setDraft({
                    ...draft,
                    socials: event.target.checked
                      ? [...draft.socials, key]
                      : draft.socials.filter((s) => s !== key),
                  })
                }
              />
              {t(key)}
            </label>
          ))}
        </fieldset>
        <div className="dialog-actions">
          <button
            type="button"
            className="outline-button"
            onClick={() => {
              setDraft(emptyFilters());
              setAgeText({ min: "", max: "" });
            }}
          >
            {t("reset")}
          </button>
          <button className="primary-button" type="submit" disabled={invalid.size > 0}>
            {t("apply")}
            <Icon name="arrow" />
          </button>
        </div>
      </form>
    </Dialog>
  );
}
