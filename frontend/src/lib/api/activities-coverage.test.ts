import { expect, it } from "vitest";
import { activitiesSchema, liquidityEventsSchema } from "./schemas";

const response = {
  items: [],
  next_cursor: null,
  coverage: "partial",
  label_counts_coverage: "partial",
  label_counts: { all: null, smart_money: null },
};

it("accepts partial activity windows without converting unknown label totals to zero", () => {
  expect(activitiesSchema.parse(response)).toMatchObject(response);
});

it("keeps activity-window and 24-hour count coverage independent", () => {
  const shortWindow = { ...response, coverage: "complete" };
  const knownLaunchCounts = {
    ...response,
    label_counts_coverage: "complete",
    label_counts: { all: 3, smart_money: 0 },
  };
  expect(activitiesSchema.parse(shortWindow)).toMatchObject(shortWindow);
  expect(activitiesSchema.parse(knownLaunchCounts)).toMatchObject(knownLaunchCounts);
});

it.each([
  { ...response, coverage: "unknown" },
  { ...response, label_counts_coverage: "unknown" },
  { ...response, label_counts: { smart_money: null } },
  { ...response, label_counts: { all: null } },
  { ...response, label_counts: { all: -1, smart_money: null } },
  { ...response, label_counts: { all: null, smart_money: 1.5 } },
])("rejects invalid activity coverage or missing/invalid required counts", (invalid) => {
  expect(activitiesSchema.safeParse(invalid).success).toBe(false);
});

it("retains liquidity coverage independently of activity label counts", () => {
  const liquidity = { items: [], next_cursor: null, coverage: "partial" };
  expect(liquidityEventsSchema.parse(liquidity)).toMatchObject(liquidity);
});
