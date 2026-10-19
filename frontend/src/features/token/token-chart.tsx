"use client";
import { useQuery } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { useEffect, useMemo, useRef, useState } from "react";
import type { IChartApi, ISeriesApi, Time, UTCTimestamp } from "lightweight-charts";
import { Icon } from "@/components/icon";
import { ErrorState } from "@/components/ui/state";
import { apiFetch } from "@/lib/api/client";
import { activitiesSchema, candlesSchema } from "@/lib/api/schemas";
import { candleIntervals, type CandleInterval, type Chain, type Currency } from "@/lib/api/types";
import { formatAmount, formatPrice } from "@/lib/format/number";
import { useAppLocale } from "@/lib/locale";
import { bollinger, ema, macd, rsi, sma, type Series } from "./indicators";

const indicatorKeys = ["MA", "EMA", "BOLL", "VOL", "MACD", "RSI"] as const;
type Indicator = (typeof indicatorKeys)[number];

const colors = {
  up: "#a5df78",
  down: "#e18c88",
  text: "#8a9284",
  grid: "#1d221a",
  line: "#2b3028",
  ma1: "#e9ce61",
  ma2: "#78a0ef",
  boll: "#b5a4c5",
};

function line(values: Series, times: UTCTimestamp[]) {
  return values.flatMap((v, i) => (v === null ? [] : [{ time: times[i], value: v }]));
}

/** K-line on the main pool (D55) with the six intervals of D105. Lazy-loads the chart lib. */
export function TokenChart({
  chain,
  address,
  currency,
  nativeSymbol,
}: {
  chain: Chain;
  address: string;
  currency: "usd" | "native";
  nativeSymbol: Currency;
}) {
  const t = useTranslations("chart");
  const locale = useAppLocale();
  const [interval, setInterval] = useState<CandleInterval>("1m");
  const [metric, setMetric] = useState<"price" | "market_cap">("price");
  const [unit, setUnit] = useState<"usd" | "native">(currency);
  const [indicators, setIndicators] = useState<Indicator[]>(["MA", "VOL"]);
  const [markers, setMarkers] = useState(true);
  const [logScale, setLogScale] = useState(false);
  const [grid, setGrid] = useState(true);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const host = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);

  const candles = useQuery({
    queryKey: ["candles", chain, address, interval, metric, unit],
    queryFn: ({ signal }) =>
      apiFetch(`/tokens/${chain}/${address}/candles`, candlesSchema, {
        signal,
        query: { interval, metric, currency: unit, limit: 300 },
      }),
    refetchInterval: interval === "1s" ? 5_000 : 15_000,
  });
  const items = useMemo(() => candles.data?.items ?? [], [candles.data]);
  const from = items[0]?.t;
  const smTrades = useQuery({
    queryKey: ["sm-markers", chain, address, from],
    queryFn: ({ signal }) =>
      apiFetch(`/tokens/${chain}/${address}/activities`, activitiesSchema, {
        signal,
        query: { kind: "trade", label: "smart_money", from, limit: 100 },
      }),
    enabled: markers && from !== undefined,
    staleTime: 15_000,
  });

  useEffect(() => {
    const onChange = () => setFullscreen(document.fullscreenElement === wrap.current);
    document.addEventListener("fullscreenchange", onChange);
    return () => document.removeEventListener("fullscreenchange", onChange);
  }, []);

  const quote: Currency = unit === "usd" ? "USD" : nativeSymbol;

  useEffect(() => {
    const el = host.current;
    if (!el || !items.length) return;
    let disposed = false;
    let chart: IChartApi | null = null;
    void import("lightweight-charts").then((lw) => {
      if (disposed) return;
      chart = lw.createChart(el, {
        autoSize: true,
        layout: {
          background: { type: lw.ColorType.Solid, color: "#11140f" },
          textColor: colors.text,
          fontFamily: "SFMono-Regular, Consolas, monospace",
          fontSize: 10,
          attributionLogo: true, // TradingView attribution required by the license
        },
        grid: {
          vertLines: { color: grid ? colors.grid : "transparent" },
          horzLines: { color: grid ? colors.grid : "transparent" },
        },
        rightPriceScale: {
          borderColor: colors.line,
          mode: logScale ? lw.PriceScaleMode.Logarithmic : lw.PriceScaleMode.Normal,
        },
        timeScale: {
          borderColor: colors.line,
          timeVisible: true,
          secondsVisible: interval === "1s" || interval === "30s",
        },
        crosshair: { mode: lw.CrosshairMode.Normal },
        localization: {
          priceFormatter: (p: number) =>
            metric === "market_cap"
              ? formatAmount(String(p), locale, quote)
              : formatPrice(p.toFixed(14).replace(/0+$/, "0"), quote),
        },
      });
      chartRef.current = chart;
      const times = items.map((c) => c.t as UTCTimestamp);
      const closes = items.map((c) => Number(c.c));
      const main = chart.addSeries(lw.CandlestickSeries, {
        upColor: colors.up,
        downColor: colors.down,
        borderVisible: false,
        wickUpColor: colors.up,
        wickDownColor: colors.down,
        priceFormat: { type: "price", minMove: 1e-12, precision: 12 },
      });
      main.setData(
        items.map((c) => ({
          time: c.t as UTCTimestamp,
          open: Number(c.o),
          high: Number(c.h),
          low: Number(c.l),
          close: Number(c.c),
        }))
      );
      const overlay = (values: Series, color: string, width: 1 | 2 = 1) => {
        const s: ISeriesApi<"Line"> = chart!.addSeries(lw.LineSeries, {
          color,
          lineWidth: width,
          priceLineVisible: false,
          lastValueVisible: false,
          crosshairMarkerVisible: false,
        });
        s.setData(line(values, times));
        return s;
      };
      if (indicators.includes("MA")) {
        overlay(sma(closes, 7), colors.ma1);
        overlay(sma(closes, 25), colors.ma2);
      }
      if (indicators.includes("EMA")) {
        overlay(ema(closes, 12), "#eb984d");
        overlay(ema(closes, 26), "#92d758");
      }
      if (indicators.includes("BOLL")) {
        const b = bollinger(closes);
        overlay(b.mid, colors.boll);
        overlay(b.upper, colors.boll);
        overlay(b.lower, colors.boll);
      }
      let pane = 1;
      if (indicators.includes("VOL")) {
        const vol = chart.addSeries(
          lw.HistogramSeries,
          { priceFormat: { type: "volume" }, priceLineVisible: false, lastValueVisible: false },
          pane++
        );
        vol.setData(
          items.map((c) => ({
            time: c.t as UTCTimestamp,
            value: Number(c.v),
            color: Number(c.c) >= Number(c.o) ? "#a5df7866" : "#e18c8866",
          }))
        );
      }
      if (indicators.includes("MACD")) {
        const m = macd(closes);
        const p = pane++;
        const hist = chart.addSeries(
          lw.HistogramSeries,
          { priceLineVisible: false, lastValueVisible: false },
          p
        );
        hist.setData(
          m.hist.flatMap((v, i) =>
            v === null
              ? []
              : [{ time: times[i], value: v, color: v >= 0 ? "#a5df7888" : "#e18c8888" }]
          )
        );
        const l1 = chart.addSeries(
          lw.LineSeries,
          { color: colors.ma1, lineWidth: 1, priceLineVisible: false, lastValueVisible: false },
          p
        );
        l1.setData(line(m.line, times));
        const l2 = chart.addSeries(
          lw.LineSeries,
          { color: colors.ma2, lineWidth: 1, priceLineVisible: false, lastValueVisible: false },
          p
        );
        l2.setData(line(m.signal, times));
      }
      if (indicators.includes("RSI")) {
        const r = chart.addSeries(
          lw.LineSeries,
          { color: colors.boll, lineWidth: 1, priceLineVisible: false, lastValueVisible: true },
          pane++
        );
        r.setData(line(rsi(closes), times));
      }
      chart.panes().forEach((p, i) => p.setStretchFactor(i === 0 ? 3 : 1));

      const step = items.length > 1 ? items[1].t - items[0].t : 60;
      const bucket = (sec: number) => (Math.floor(sec / step) * step) as UTCTimestamp;
      const marks: {
        time: Time;
        position: "aboveBar" | "belowBar";
        color: string;
        shape: "arrowUp" | "arrowDown" | "square";
        text: string;
      }[] = [];
      if (markers) {
        for (const a of smTrades.data?.items ?? []) {
          const sec = Math.floor(Date.parse(a.time) / 1000);
          if (sec < items[0].t) continue;
          marks.push({
            time: bucket(sec),
            position: a.kind === "buy" ? "belowBar" : "aboveBar",
            color: a.kind === "buy" ? colors.up : colors.down,
            shape: a.kind === "buy" ? "arrowUp" : "arrowDown",
            text: a.kind === "buy" ? "B" : "S",
          });
        }
      }
      for (const s of candles.data?.pool_switches ?? []) {
        marks.push({
          time: bucket(s.t),
          position: "aboveBar",
          color: "#b5f268",
          shape: "square",
          text: s.reason === "graduation" ? t("graduation") : t("poolSwitch"),
        });
      }
      marks.sort((a, b) => (a.time as number) - (b.time as number));
      if (marks.length) lw.createSeriesMarkers(main, marks);
      chart.timeScale().fitContent();
    });
    return () => {
      disposed = true;
      chart?.remove();
      chartRef.current = null;
    };
  }, [
    items,
    indicators,
    markers,
    smTrades.data,
    logScale,
    grid,
    interval,
    metric,
    quote,
    locale,
    candles.data,
    t,
  ]);

  function screenshot() {
    const canvas = chartRef.current?.takeScreenshot();
    if (!canvas) return;
    const a = document.createElement("a");
    a.href = canvas.toDataURL("image/png");
    a.download = `${chain}-${address.slice(0, 6)}-${interval}.png`;
    a.click();
  }

  async function toggleFullscreen() {
    if (document.fullscreenElement) await document.exitFullscreen();
    else await wrap.current?.requestFullscreen().catch(() => undefined);
  }

  return (
    <section
      className={`chart-panel ${fullscreen ? "is-fullscreen" : ""}`}
      ref={wrap}
      aria-label={t("interval")}
    >
      <div className="chart-toolbar">
        <div className="time-selector" role="group" aria-label={t("interval")}>
          {candleIntervals.map((i) => (
            <button
              type="button"
              key={i}
              aria-pressed={interval === i}
              className={interval === i ? "selected" : ""}
              onClick={() => setInterval(i)}
            >
              {i}
            </button>
          ))}
        </div>
        <div className="segmented" role="group" aria-label={t("metric")}>
          {(["price", "market_cap"] as const).map((m) => (
            <button
              type="button"
              key={m}
              aria-pressed={metric === m}
              className={metric === m ? "selected" : ""}
              onClick={() => setMetric(m)}
            >
              {m === "price" ? t("price") : t("marketCap")}
            </button>
          ))}
        </div>
        <div className="segmented" role="group" aria-label={t("currency")}>
          {(["usd", "native"] as const).map((u) => (
            <button
              type="button"
              key={u}
              aria-pressed={unit === u}
              className={unit === u ? "selected" : ""}
              onClick={() => setUnit(u)}
            >
              {u === "usd" ? "USD" : nativeSymbol}
            </button>
          ))}
        </div>
        <div className="chart-actions">
          <button
            type="button"
            className="icon-button"
            aria-label={t("screenshot")}
            title={t("screenshot")}
            onClick={screenshot}
          >
            <Icon name="copy" size={15} />
          </button>
          <button
            type="button"
            className="icon-button"
            aria-label={fullscreen ? t("exitFullscreen") : t("fullscreen")}
            title={fullscreen ? t("exitFullscreen") : t("fullscreen")}
            onClick={() => void toggleFullscreen()}
          >
            <Icon name="grid" size={15} />
          </button>
          <button
            type="button"
            className="icon-button"
            aria-label={t("settings")}
            aria-expanded={settingsOpen}
            onClick={() => setSettingsOpen(!settingsOpen)}
          >
            <Icon name="settings" size={15} />
          </button>
        </div>
      </div>
      <div className="chart-subbar">
        <div className="indicator-toggles" role="group" aria-label={t("indicators")}>
          {indicatorKeys.map((k) => (
            <button
              type="button"
              key={k}
              aria-pressed={indicators.includes(k)}
              className={indicators.includes(k) ? "selected" : ""}
              onClick={() =>
                setIndicators(
                  indicators.includes(k) ? indicators.filter((x) => x !== k) : [...indicators, k]
                )
              }
            >
              {k}
            </button>
          ))}
          <label className="checkbox-label inline">
            <input
              type="checkbox"
              checked={markers}
              onChange={(e) => setMarkers(e.target.checked)}
            />
            {t("markers")}
          </label>
        </div>
        <span className="muted">{t("mainPoolNote")}</span>
      </div>
      {settingsOpen ? (
        <div className="chart-settings">
          <label className="checkbox-label inline">
            <input
              type="checkbox"
              checked={logScale}
              onChange={(e) => setLogScale(e.target.checked)}
            />
            {t("logScale")}
          </label>
          <label className="checkbox-label inline">
            <input type="checkbox" checked={grid} onChange={(e) => setGrid(e.target.checked)} />
            {t("grid")}
          </label>
        </div>
      ) : null}
      <div className="chart-host" ref={host} data-testid="token-chart">
        {candles.isPending ? <span className="chart-note">{t("loading")}</span> : null}
        {candles.isSuccess && !items.length ? (
          <span className="chart-note">{t("empty")}</span>
        ) : null}
      </div>
      {candles.isError ? (
        <ErrorState
          title={t("error")}
          error={candles.error}
          onRetry={() => void candles.refetch()}
        />
      ) : null}
    </section>
  );
}
