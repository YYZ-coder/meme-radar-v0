export const locales = ["zh-CN", "en"] as const;
export type AppLocale = (typeof locales)[number];
export const defaultLocale: AppLocale = "zh-CN";
/** Cookie that remembers the UI language (B11, 14-frontend §5). */
export const localeCookie = "NEXT_LOCALE";

export function isLocale(value: unknown): value is AppLocale {
  return typeof value === "string" && (locales as readonly string[]).includes(value);
}

/** Cookie → Accept-Language → zh-CN. */
export function resolveLocale(
  cookie: string | undefined,
  acceptLanguage: string | null
): AppLocale {
  if (isLocale(cookie)) return cookie;
  for (const part of (acceptLanguage ?? "").split(",")) {
    const tag = part.split(";")[0].trim().toLowerCase();
    if (tag.startsWith("zh")) return "zh-CN";
    if (tag.startsWith("en")) return "en";
  }
  return defaultLocale;
}
