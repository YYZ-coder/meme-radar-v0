import type { CSSProperties } from "react";
export type IconName =
  | "radar"
  | "grid"
  | "star"
  | "signal"
  | "wallet"
  | "settings"
  | "help"
  | "search"
  | "arrow"
  | "filter"
  | "close"
  | "copy"
  | "chevron"
  | "pause"
  | "play"
  | "globe"
  | "check"
  | "sort"
  | "external"
  | "x"
  | "send"
  | "code"
  | "lock";
const paths: Record<IconName, string> = {
  radar: "M12 3v3M12 18v3M3 12h3M18 12h3M12 12l6-6M8 5H5v3M16 5h3v3M5 16v3h3M19 16v3h-3M9 9h6v6H9z",
  grid: "M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z",
  star: "m12 3 3 6 6 1-4 5 1 6-6-3-6 3 1-6-4-5 6-1z",
  signal: "M4 17v3M9 12v8M14 7v13M19 3v17",
  wallet: "M4 6h15v14H4zM4 6V3h12v3M14 11h7v5h-7zM17 13h1",
  settings: "M9 3h6v4l3 2 3 1v5l-4 1-2 3v2H9v-3l-3-2H3v-6l4-1 2-3zM10 10h4v4h-4z",
  help: "M8 3h8l5 5v8l-5 5H8l-5-5V8zM9 9V7h6v4l-3 2v2M12 17v1",
  search: "M4 4h10v10H4zM14 14l7 7",
  arrow: "M4 12h15M13 5l7 7-7 7",
  filter: "M3 6h18M6 12h12M9 18h6M8 3v6M16 9v6M11 15v6",
  close: "m5 5 14 14M19 5 5 19",
  copy: "M9 9h11v11H9zM5 15H3V3h12v2",
  chevron: "m6 9 6 6 6-6",
  pause: "M8 5v14M16 5v14",
  play: "m8 4 12 8-12 8z",
  globe: "M8 3h8l5 5v8l-5 5H8l-5-5V8zM3 12h18M12 3v18M8 3l-2 9 2 9M16 3l2 9-2 9",
  check: "m4 12 5 5L20 6",
  sort: "M8 4v16M4 8l4-4 4 4M16 20V4M12 16l4 4 4-4",
  external: "M14 3h7v7M21 3 11 13M10 5H3v16h16v-7",
  x: "M5 4h4l10 16h-4zM19 4l-6 7M5 20l6-7",
  send: "M3 11 21 3l-8 18-2-8z",
  code: "M8 7l-5 5 5 5M16 7l5 5-5 5",
  lock: "M6 11h12v10H6zM9 11V7h6v4M12 15v2",
};
export function Icon({
  name,
  size = 18,
  style,
}: {
  name: IconName;
  size?: number;
  style?: CSSProperties;
}) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="square"
      strokeLinejoin="miter"
      aria-hidden="true"
      style={style}
    >
      <path d={paths[name]} />
    </svg>
  );
}
