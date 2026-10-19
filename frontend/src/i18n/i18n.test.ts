import { describe, expect, it } from "vitest";
import en from "@/messages/en.json";
import zh from "@/messages/zh-CN.json";
import { errorCodes } from "@/lib/api/types";
import { resolveLocale } from "./config";

function keys(value: unknown, prefix = ""): string[] {
  if (!value || typeof value !== "object") return [prefix];
  return Object.entries(value).flatMap(([k, v]) => keys(v, prefix ? `${prefix}.${k}` : k));
}

describe("i18n", () => {
  it("keeps both dictionaries on the same key set", () => {
    expect(keys(en).sort()).toEqual(keys(zh).sort());
  });
  it("uses the same ICU placeholders in both languages", () => {
    const placeholders = (text: string) => (text.match(/\{\w+\}/g) ?? []).sort();
    const flat = (obj: Record<string, unknown>, out: Record<string, string> = {}, p = "") => {
      for (const [k, v] of Object.entries(obj)) {
        if (typeof v === "string") out[p + k] = v;
        else flat(v as Record<string, unknown>, out, `${p}${k}.`);
      }
      return out;
    };
    const fz = flat(zh);
    const fe = flat(en);
    for (const key of Object.keys(fz))
      expect(placeholders(fe[key]), key).toEqual(placeholders(fz[key]));
  });
  it("has a message for every API error code and client error", () => {
    for (const code of [...errorCodes, "network", "invalid_response"]) {
      expect(zh.errors, code).toHaveProperty(code);
      expect(en.errors, code).toHaveProperty(code);
    }
  });
  it("resolves cookie, then Accept-Language, then zh-CN", () => {
    expect(resolveLocale("en", "zh-CN,zh")).toBe("en");
    expect(resolveLocale(undefined, "en-US,en;q=0.9")).toBe("en");
    expect(resolveLocale("fr", "fr-FR")).toBe("zh-CN");
    expect(resolveLocale(undefined, null)).toBe("zh-CN");
  });
});
