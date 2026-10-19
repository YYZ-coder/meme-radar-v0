import Link from "next/link";
import { getTranslations } from "next-intl/server";

export default async function NotFound() {
  const t = await getTranslations("states");
  return (
    <div className="empty-state page-empty">
      <h1>{t("notFoundTitle")}</h1>
      <p>{t("notFoundHint")}</p>
      <Link className="outline-button" href="/smart-money/inflow">
        {t("goHome")}
      </Link>
    </div>
  );
}
