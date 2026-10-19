import { fireEvent, render, screen } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { describe, expect, it, vi } from "vitest";
import zh from "@/messages/zh-CN.json";
import RouteError from "./error";

describe("route error boundary", () => {
  it("shows the crash message, digest and a working retry", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const reset = vi.fn();
    render(
      <NextIntlClientProvider locale="zh-CN" messages={zh}>
        <RouteError error={Object.assign(new Error("boom"), { digest: "abc123" })} reset={reset} />
      </NextIntlClientProvider>
    );
    expect(screen.getByRole("alert")).toHaveTextContent("页面出错了");
    expect(screen.getByText(/abc123/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(reset).toHaveBeenCalledOnce();
  });
});
