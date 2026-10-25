package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestLiquidityStepSamplesReservesAndAveragesTheLastHour(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	now := day.Add(12 * time.Hour)
	wsol, usdc, meme := []byte("wsol"), []byte("usdc"), []byte("meme")
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{
		{Chain: "solana", Asset: wsol, Symbol: "SOL", Kind: "native", Decimals: 9},
		{Chain: "solana", Asset: usdc, Symbol: "USDC", Kind: "stable", Decimals: 6},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: now.Truncate(time.Minute), USD: "200", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	pool := func(id, quote []byte, protocol, rq string, tx int) parse.Pool {
		return parse.Pool{ID: id, Token: meme, Quote: quote, QuoteSymbol: "x", Protocol: protocol, Dex: "d", Created: true, Status: "active",
			ReserveToken: map[bool]string{true: "1", false: ""}[rq != ""], ReserveQuote: rq, Tx: tx, Ix: 1}
	}
	l := compute.NewLiquidity(s, []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}})
	step := func(at time.Time) {
		t.Helper()
		if err := l.Step(ctx, at); err != nil {
			t.Fatal(err)
		}
	}
	// Curve A: 10 SOL raised 90 minutes ago, 20 SOL 30 minutes ago.
	apply(t, s, "solana", 1, now.Add(-90*time.Minute), parse.Facts{Pools: []parse.Pool{pool([]byte("A"), wsol, "pump_curve", "10000000000", 1)}})
	step(now.Add(-89 * time.Minute))
	apply(t, s, "solana", 2, now.Add(-30*time.Minute), parse.Facts{Pools: []parse.Pool{pool([]byte("A"), wsol, "pump_curve", "20000000000", 1)}})
	// AMM B: 500 USDC reserve since 10 minutes ago; v4 pool C and pool D (unregistered quote) have no usable reserves.
	apply(t, s, "solana", 3, now.Add(-10*time.Minute), parse.Facts{Pools: []parse.Pool{
		pool([]byte("B"), usdc, "pumpswap", "500000000", 1), pool([]byte("C"), wsol, "uniswap_v4", "", 2), pool([]byte("D"), []byte("other"), "pumpswap", "7", 3)}})
	step(now)

	got := func(id string) string {
		return one(t, c, `SELECT concat_ws(' ', coalesce(trim_scale(liquidity_usd)::text, 'NULL'), coalesce(trim_scale(liquidity_native)::text, 'NULL'),
			coalesce(trim_scale(liquidity_1h_avg_usd)::text, 'NULL')) FROM pools LEFT JOIN pool_market USING (chain, pool_id) WHERE chain = 'solana' AND pool_id = $1`, []byte(id))
	}
	for id, want := range map[string]string{
		// 40 SOL now (8000 USD); the hour held 20 SOL for 30 min and 40 SOL for 30 min: 30 SOL = 6000 USD.
		"A": "8000 40 6000",
		// 1000 USDC = 1000 USD = 5 SOL; sampled only for the last 10 minutes.
		"B": "1000 5 1000",
		"C": "NULL NULL NULL",
		"D": "NULL NULL NULL",
	} {
		if g := got(id); g != want {
			t.Errorf("pool %s liquidity (usd native 1h-avg) = %s; want %s", id, g, want)
		}
	}
}
