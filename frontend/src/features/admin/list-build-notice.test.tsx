import { cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api/client";
import messages from "@/messages/zh-CN.json";
import { ListBuildNotice } from "./list-build-notice";
const call = vi.hoisted(() => vi.fn());
vi.mock("./admin-context", () => ({ useAdmin: () => ({ call }) }));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
it("shows the translated error code and a copyable trace, without rendering the backend message", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const trace = "0123456789abcdef0123456789abcdef";
  call.mockRejectedValue(
    new ApiError(503, "api_unavailable", "private backend detail", null, null, trace)
  );
  render(
    <NextIntlClientProvider locale="zh-CN" messages={messages}>
      <QueryClientProvider client={client}>
        <ListBuildNotice version="43" />
      </QueryClientProvider>
    </NextIntlClientProvider>
  );
  expect(await screen.findByText(messages.errors.api_unavailable)).toBeVisible();
  expect(screen.queryByText(/private backend detail/)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: new RegExp(trace) })).toBeVisible();
  client.clear();
});
