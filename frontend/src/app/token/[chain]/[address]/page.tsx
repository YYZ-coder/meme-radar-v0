import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { TokenView } from "@/features/token/token-view";
import { isAddress, isChain } from "@/lib/chains";

type Props = { params: Promise<{ chain: string; address: string }> };

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { address } = await params;
  return { title: `${address.slice(0, 4)}…${address.slice(-4)}` };
}

export default async function TokenPage({ params }: Props) {
  const { chain, address } = await params;
  if (!isChain(chain) || !isAddress(chain, address)) notFound();
  return <TokenView chain={chain} address={address} />;
}
