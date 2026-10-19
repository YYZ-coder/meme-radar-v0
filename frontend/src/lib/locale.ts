"use client";
import { useLocale } from "next-intl";
import type { Locale } from "./format/number";

export function useAppLocale(): Locale {
  return useLocale() === "en" ? "en" : "zh-CN";
}
