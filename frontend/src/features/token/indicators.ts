// Chart overlays computed from candle closes (display only). Periods follow common defaults:
// MA 7/25, EMA 12/26, BOLL 20±2σ, RSI 14 (Wilder), MACD 12/26/9.
export type Series = (number | null)[];

export function sma(values: number[], period: number): Series {
  const out: Series = [];
  let sum = 0;
  for (let i = 0; i < values.length; i++) {
    sum += values[i];
    if (i >= period) sum -= values[i - period];
    out.push(i >= period - 1 ? sum / period : null);
  }
  return out;
}

export function ema(values: number[], period: number): Series {
  const out: Series = [];
  const k = 2 / (period + 1);
  let prev: number | null = null;
  for (let i = 0; i < values.length; i++) {
    if (i < period - 1) {
      out.push(null);
      continue;
    }
    if (prev === null) {
      prev = values.slice(0, period).reduce((a, b) => a + b, 0) / period;
    } else {
      prev = values[i] * k + prev * (1 - k);
    }
    out.push(prev);
  }
  return out;
}

export function bollinger(values: number[], period = 20, mult = 2) {
  const mid = sma(values, period);
  const upper: Series = [];
  const lower: Series = [];
  for (let i = 0; i < values.length; i++) {
    const m = mid[i];
    if (m === null) {
      upper.push(null);
      lower.push(null);
      continue;
    }
    const window = values.slice(i - period + 1, i + 1);
    const sd = Math.sqrt(window.reduce((acc, v) => acc + (v - m) ** 2, 0) / period);
    upper.push(m + mult * sd);
    lower.push(m - mult * sd);
  }
  return { mid, upper, lower };
}

export function rsi(values: number[], period = 14): Series {
  const out: Series = values.map(() => null);
  if (values.length <= period) return out;
  let gain = 0;
  let loss = 0;
  for (let i = 1; i <= period; i++) {
    const d = values[i] - values[i - 1];
    if (d >= 0) gain += d;
    else loss -= d;
  }
  gain /= period;
  loss /= period;
  out[period] = loss === 0 ? 100 : 100 - 100 / (1 + gain / loss);
  for (let i = period + 1; i < values.length; i++) {
    const d = values[i] - values[i - 1];
    gain = (gain * (period - 1) + Math.max(d, 0)) / period;
    loss = (loss * (period - 1) + Math.max(-d, 0)) / period;
    out[i] = loss === 0 ? 100 : 100 - 100 / (1 + gain / loss);
  }
  return out;
}

export function macd(values: number[], fast = 12, slow = 26, signalPeriod = 9) {
  const f = ema(values, fast);
  const s = ema(values, slow);
  const line: Series = values.map((_, i) => {
    const a = f[i];
    const b = s[i];
    return a === null || b === null ? null : a - b;
  });
  const start = line.findIndex((v) => v !== null);
  const signal: Series = values.map(() => null);
  if (start >= 0) {
    const compact = line.slice(start) as number[];
    ema(compact, signalPeriod).forEach((v, i) => {
      signal[start + i] = v;
    });
  }
  const hist: Series = line.map((v, i) => {
    const sg = signal[i];
    return v === null || sg === null ? null : v - sg;
  });
  return { line, signal, hist };
}
