"use client";
// Last-resort fallback when the root layout itself fails; i18n is unavailable here,
// so the copy is bilingual and kept outside messages on purpose.
/* eslint-disable no-restricted-syntax */
import "./globals.css";

export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <html lang="zh-CN">
      <body>
        <main className="state-block error crash-page" role="alert">
          <strong>页面出错了 / Something broke</strong>
          {error.digest ? <code className="trace-id">digest: {error.digest}</code> : null}
          <button type="button" className="outline-button" onClick={reset}>
            重试 / Retry
          </button>
        </main>
      </body>
    </html>
  );
}
