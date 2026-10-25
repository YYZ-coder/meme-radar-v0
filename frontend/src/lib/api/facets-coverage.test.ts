import { expect, it } from "vitest";
import { facetsSchema } from "./schemas";

const facet = {
  id: "uniswap_v4",
  name: "Uniswap v4",
  chain: "base",
  token_count: null,
  observed_token_count: 2,
  volume_usd: null,
};
const partial = { coverage: "partial", dexes: [facet], platforms: [] };

it("preserves parsed facet observations while complete totals remain unknown", () => {
  expect(facetsSchema.parse(partial)).toMatchObject({
    coverage: "partial",
    dexes: [{ token_count: null, volume_usd: null, observed_token_count: 2 }],
  });
});

it("keeps complete facet counts separate from parsed counts", () => {
  const complete = {
    coverage: "complete",
    dexes: [{ ...facet, token_count: 3, observed_token_count: 2, volume_usd: "12" }],
    platforms: [],
  };
  expect(facetsSchema.parse(complete)).toMatchObject(complete);
});

it.each([
  { ...partial, coverage: "unknown" },
  { ...partial, dexes: [{ ...facet, observed_token_count: -1 }] },
  { ...partial, dexes: [{ ...facet, observed_token_count: 1.5 }] },
  { ...partial, dexes: [{ ...facet, observed_token_count: undefined }] },
])("rejects untrusted facet observations and coverage", (invalid) => {
  expect(facetsSchema.safeParse(invalid).success).toBe(false);
});
