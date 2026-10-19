"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useTranslations } from "next-intl";
import {
  createContext,
  useContext,
  useState,
  useSyncExternalStore,
  type FormEvent,
  type ReactNode,
} from "react";
import { Icon } from "@/components/icon";
import { apiFetch, isApiError, isMockApi } from "@/lib/api/client";
import { adminHealthSchema } from "@/lib/api/schemas";

// Admin token lives in sessionStorage only (cleared with the tab), never in URL or localStorage.
const KEY = "meme-radar:admin-token";
const EVENT = "radar-admin-token";

function readToken() {
  try {
    return sessionStorage.getItem(KEY) ?? "";
  } catch {
    return "";
  }
}
function writeToken(token: string) {
  try {
    if (token) sessionStorage.setItem(KEY, token);
    else sessionStorage.removeItem(KEY);
  } catch {
    /* storage unavailable: token only lives for this render */
  }
  window.dispatchEvent(new Event(EVENT));
}
function subscribe(cb: () => void) {
  window.addEventListener(EVENT, cb);
  return () => window.removeEventListener(EVENT, cb);
}

const AdminContext = createContext<{ token: string; signOut: () => void }>({
  token: "",
  signOut: () => {},
});

export function useAdmin() {
  const { token, signOut } = useContext(AdminContext);
  /** Typed fetch with the bearer token; a 403 signs out. */
  const call: typeof apiFetch = async (path, schema, init = {}) => {
    try {
      return await apiFetch(path, schema, { ...init, token });
    } catch (error) {
      if (isApiError(error, 403)) signOut();
      throw error;
    }
  };
  return { token, call, signOut };
}

function Gate() {
  const t = useTranslations("admin");
  const [value, setValue] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      await apiFetch("/admin/health", adminHealthSchema, { token: value.trim() });
      writeToken(value.trim());
    } catch (e) {
      setError(isApiError(e, 403) ? t("forbidden") : e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="admin-gate side-card" onSubmit={submit}>
      <h2>{t("title")}</h2>
      <p className="muted">{t("tokenHint")}</p>
      <label className="field-label">
        {t("tokenLabel")}
        <input
          type="password"
          autoComplete="off"
          value={value}
          onChange={(event) => setValue(event.target.value)}
          required
        />
      </label>
      {isMockApi() ? <p className="panel-note">{t("tokenMockHint")}</p> : null}
      {error ? (
        <p className="negative" role="alert">
          {error}
        </p>
      ) : null}
      <button className="primary-button" type="submit" disabled={busy || !value.trim()}>
        {t("signIn")}
        <Icon name="arrow" size={15} />
      </button>
    </form>
  );
}

export function AdminShell({ children }: { children: ReactNode }) {
  const t = useTranslations("admin");
  const pathname = usePathname();
  const token = useSyncExternalStore(subscribe, readToken, () => "");
  const tabs = [
    { href: "/admin/health", label: t("tabHealth") },
    { href: "/admin/import", label: t("tabImport") },
    { href: "/admin/members", label: t("tabMembers") },
  ];
  return (
    <div className="admin-page">
      <div className="page-intro">
        <div>
          <div className="eyebrow">
            <span className="small-square" /> {t("eyebrow")}
          </div>
          <h1>{t("title")}</h1>
          <p>{t("subtitle")}</p>
        </div>
        {token ? (
          <button type="button" className="outline-button bordered" onClick={() => writeToken("")}>
            {t("signOut")}
          </button>
        ) : null}
      </div>
      {token ? (
        <AdminContext.Provider value={{ token, signOut: () => writeToken("") }}>
          <section className="market-panel">
            <div className="market-nav">
              <nav className="view-tabs" aria-label={t("title")}>
                {tabs.map((tab) => (
                  <Link
                    key={tab.href}
                    href={tab.href}
                    className={pathname === tab.href ? "selected" : ""}
                    aria-current={pathname === tab.href ? "page" : undefined}
                  >
                    {tab.label}
                  </Link>
                ))}
              </nav>
            </div>
            <div className="admin-body">{children}</div>
          </section>
        </AdminContext.Provider>
      ) : (
        <Gate />
      )}
    </div>
  );
}
