// @vitest-environment node
import { describe, expect, it } from "vitest";
import { batchSchema, inflowPageSchema } from "./schemas";

const times = { base: "2026-10-21T10:00:00Z", bsc: "2026-10-21T10:00:05Z" };
const page = {
  query_id: "fixed-query",
  list_version: "1",
  as_of: times.bsc,
  chain_as_of: times,
  window: "5m",
  total: 0,
  truncated: false,
  offset: 0,
  next_offset: null,
  items: [],
};

describe("per-chain snapshot times", () => {
  it("preserves different chain times through list and batch validation", () => {
    expect(inflowPageSchema.parse(page)).toMatchObject({ chain_as_of: times });
    expect(batchSchema.parse(page)).toMatchObject({ chain_as_of: times });
  });

  it("rejects an unsupported chain or non-text chain time", () => {
    for (const chain_as_of of [{ eth: times.base }, { base: 0 }]) {
      expect(inflowPageSchema.safeParse({ ...page, chain_as_of }).success).toBe(false);
      expect(batchSchema.safeParse({ ...page, chain_as_of }).success).toBe(false);
    }
  });
});
