import { PixelArt, spriteNames } from "@/components/pixel-art";
import { ChainMark } from "@/components/ui/chain-mark";
import type { Chain } from "@/lib/api/types";
import { hash } from "@/lib/hash";

const palette = [
  "#92d758",
  "#d5b18a",
  "#6e9dff",
  "#d6b788",
  "#e7b759",
  "#eb984d",
  "#78a0ef",
  "#b5a4c5",
  "#e9ce61",
  "#f1d367",
  "#a4b5f6",
  "#c5a5d9",
];

/** Pixel avatar derived from the address (03-leaderboard: 按地址生成像素头像). */
export function avatarFor(address: string) {
  const h = hash(address.toLowerCase());
  return { art: spriteNames[h % spriteNames.length], color: palette[(h >>> 8) % palette.length] };
}

export function TokenAvatar({
  chain,
  address,
  size = 36,
  withChain = true,
}: {
  chain: Chain;
  address: string;
  size?: number;
  withChain?: boolean;
}) {
  const { art, color } = avatarFor(address);
  return (
    <span className="avatar-with-chain">
      <PixelArt art={art} color={color} size={size} />
      {withChain ? <ChainMark chain={chain} /> : null}
    </span>
  );
}
