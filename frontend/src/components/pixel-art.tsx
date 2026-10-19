import type { CSSProperties } from "react";
const sprites: Record<string, string[]> = {
  frog: [
    "000111111000",
    "001111111100",
    "011221122110",
    "011231132110",
    "111111111111",
    "111111111111",
    "111411114111",
    "011444444110",
    "001111111100",
    "000111111000",
  ],
  dog: [
    "000055550000",
    "000555555000",
    "005555555500",
    "066666666660",
    "001111111100",
    "011111111110",
    "012311132110",
    "011111111110",
    "001144111100",
    "000144441000",
  ],
  cat: [
    "010000000010",
    "011000000110",
    "011111111110",
    "111111111111",
    "112311113211",
    "111111111111",
    "011144441110",
    "001144441100",
    "000144441000",
    "000011110000",
  ],
  boy: [
    "000111111000",
    "001111111100",
    "011111111110",
    "011221122110",
    "111231132111",
    "111111111111",
    "111144441111",
    "011144441110",
    "001111111100",
    "000111111000",
  ],
  viking: [
    "020000000020",
    "022055550220",
    "002555555200",
    "005555555500",
    "011111111110",
    "012311132110",
    "011111111110",
    "001444441100",
    "001144441100",
    "000111111000",
  ],
  hippo: [
    "000110011000",
    "001111111100",
    "011111111110",
    "012311132110",
    "111111111111",
    "111221122111",
    "111111111111",
    "011144441110",
    "001111111100",
    "000111111000",
  ],
  banana: [
    "000000011000",
    "000000111000",
    "000001111000",
    "000011111000",
    "000111111000",
    "001111110000",
    "011111100000",
    "011111000000",
    "001110000000",
    "000000000000",
  ],
  alien: [
    "001000000100",
    "000100001000",
    "001111111100",
    "011011110110",
    "111111111111",
    "110111111011",
    "110111111011",
    "001001001000",
    "010010010100",
    "100000000010",
  ],
};
export const spriteNames = Object.keys(sprites);

export function PixelArt({
  art = "frog",
  color = "#b4ef63",
  size = 40,
}: {
  art?: string;
  color?: string;
  size?: number;
}) {
  const palette: Record<string, string> = {
    "1": color,
    "2": "#f0f2d4",
    "3": "#232921",
    "4": "#3c4230",
    "5": "#e3bfc6",
    "6": "#ac7f89",
  };
  return (
    <span
      className="pixel-avatar"
      style={{ "--avatar-color": color, width: size, height: size } as CSSProperties}
    >
      <svg viewBox="0 0 14 14" aria-hidden="true" shapeRendering="crispEdges">
        {(sprites[art] ?? sprites.alien).flatMap((row, y) =>
          row
            .split("")
            .map((pixel, x) =>
              pixel !== "0" ? (
                <rect
                  key={`${x}-${y}`}
                  x={x + 1}
                  y={y + 2}
                  width="1"
                  height="1"
                  fill={palette[pixel]}
                />
              ) : null
            )
        )}
      </svg>
    </span>
  );
}
/** Normalizes raw values (nulls are skipped) into the 0–100 band the polyline expects. */
export function normalizePoints(values: (number | null)[]): number[] {
  const known = values.filter((v): v is number => v !== null && Number.isFinite(v));
  if (known.length < 2) return [];
  const min = Math.min(...known);
  const max = Math.max(...known);
  const span = max - min || 1;
  let last = known[0];
  return values.map((v) => {
    if (v !== null && Number.isFinite(v)) last = v;
    return ((last - min) / span) * 90 + 5;
  });
}

export function Sparkline({
  points,
  negative = false,
  large = false,
}: {
  points: number[];
  negative?: boolean;
  large?: boolean;
}) {
  if (points.length < 2) {
    return <span className="sparkline-empty mono">--</span>;
  }
  const coordinates = points
    .map((p, i) => `${(i * 120) / (points.length - 1)},${48 - p * 0.42}`)
    .join(" ");
  return (
    <svg
      className={`sparkline ${negative ? "negative" : ""} ${large ? "large" : ""}`}
      viewBox="0 0 120 52"
      preserveAspectRatio="none"
      aria-hidden="true"
    >
      <polyline
        points={coordinates}
        fill="none"
        stroke="currentColor"
        strokeWidth={large ? "1" : "1.6"}
        strokeLinejoin="miter"
        vectorEffect="non-scaling-stroke"
      />
      {large ? (
        <polygon points={`0,52 ${coordinates} 120,52`} fill="currentColor" opacity=".06" />
      ) : null}
    </svg>
  );
}
export function Radar({ quiet = false, tag }: { quiet?: boolean; tag: string }) {
  return (
    <div className={`radar ${quiet ? "quiet" : ""}`} aria-hidden="true">
      <div className="radar-grid" />
      <div className="radar-ring ring-1" />
      <div className="radar-ring ring-2" />
      <div className="radar-ring ring-3" />
      <div className="radar-cross horizontal" />
      <div className="radar-cross vertical" />
      <div className="radar-sweep" />
      <span className="radar-dot dot-1" />
      <span className="radar-dot dot-2" />
      <span className="radar-dot dot-3" />
      <span className="radar-dot dot-4" />
      <span className="radar-center" />
      <span className="radar-tag">{tag}</span>
    </div>
  );
}
