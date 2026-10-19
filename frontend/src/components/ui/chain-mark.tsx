import type { Chain } from "@/lib/api/types";

export function ChainMark({ chain }: { chain: Chain }) {
  return (
    <span aria-hidden="true" className={`chain-mark chain-${chain}`}>
      {chain === "solana" ? (
        <>
          <i />
          <i />
          <i />
        </>
      ) : chain === "base" ? (
        <i />
      ) : (
        <>
          <i />
          <i />
          <i />
          <i />
        </>
      )}
    </span>
  );
}
