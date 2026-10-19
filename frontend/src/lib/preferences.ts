import { createLocalStore } from "./local-store";

export type Preferences = { reduceMotion: boolean; currency: "usd" | "native" };
const defaults: Preferences = { reduceMotion: false, currency: "usd" };

const store = createLocalStore<Preferences>("meme-radar:prefs:v1", defaults, (raw) => {
  const v = (raw ?? {}) as Partial<Preferences>;
  return {
    reduceMotion: v.reduceMotion === true,
    currency: v.currency === "native" ? "native" : "usd",
  };
});

export function usePreferences() {
  const prefs = store.useValue();
  const update = (patch: Partial<Preferences>) => store.write({ ...store.read(), ...patch });
  return [prefs, update] as const;
}
