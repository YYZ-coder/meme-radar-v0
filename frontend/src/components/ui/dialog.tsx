"use client";
import { useEffect, useRef, type ReactNode } from "react";
import { useTranslations } from "next-intl";
import { Icon } from "@/components/icon";

/** Pixel-styled native <dialog>: focus trap, Escape and backdrop close come from the platform. */
export function Dialog({
  title,
  eyebrow = "MEME RADAR",
  onClose,
  children,
  wide = false,
}: {
  title: string;
  eyebrow?: string;
  onClose: () => void;
  children: ReactNode;
  wide?: boolean;
}) {
  const t = useTranslations("common");
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current;
    dialog?.showModal();
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      dialog?.close();
      document.body.style.overflow = previous;
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className={`pixel-dialog ${wide ? "wide" : ""}`}
      aria-labelledby="dialog-title"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onClick={(event) => {
        if (event.target !== event.currentTarget) return;
        const rect = event.currentTarget.getBoundingClientRect();
        if (
          event.clientX < rect.left ||
          event.clientX > rect.right ||
          event.clientY < rect.top ||
          event.clientY > rect.bottom
        )
          onClose();
      }}
    >
      <div className="dialog-heading">
        <span className="eyebrow">{eyebrow}</span>
        <button type="button" className="icon-button" aria-label={t("close")} onClick={onClose}>
          <Icon name="close" />
        </button>
      </div>
      <h2 id="dialog-title">{title}</h2>
      {children}
    </dialog>
  );
}
