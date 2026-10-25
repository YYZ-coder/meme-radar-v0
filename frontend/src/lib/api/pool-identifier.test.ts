import { expect, test } from "vitest";
import { poolIdentifierSchema, tokenRefSchema } from "./schemas";

test("preserves bytes32 Uniswap v4 IDs without relaxing wallet or token addresses", () => {
  const id = "0x" + "ab".repeat(32);
  expect(poolIdentifierSchema.parse(id)).toBe(id);
  expect(poolIdentifierSchema.safeParse("0x" + "ab".repeat(31)).success).toBe(false);
  expect(
    tokenRefSchema.safeParse({ chain: "base", address: id, symbol: "X", name: "X" }).success
  ).toBe(false);
});
