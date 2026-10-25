package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestStatsStepAggregatesPoolMinutesAndFollowsChanges(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	now := day.Add(12 * time.Hour)
	m0 := now.Add(-10 * time.Minute).Truncate(time.Minute)
	wsol, usdc, m := []byte("wsol"), []byte("usdc"), []byte("M")
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{
		{Chain: "solana", Asset: wsol, Symbol: "SOL", Kind: "native", Decimals: 9},
		{Chain: "solana", Asset: usdc, Symbol: "USDC", Kind: "stable", Decimals: 6},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: m0, USD: "200", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	tr := func(tx int, pool, side, quote string) parse.Trade {
		x := trade(tx, []byte(pool), m, "1000000", quote, "")
		x.Side = side
		return x
	}
	facts := parse.Facts{
		Tokens: []parse.Token{{Address: m, Created: true, Symbol: "M", Decimals: &six}},
		Pools: []parse.Pool{{ID: []byte("A"), Token: m, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"},
			{ID: []byte("B"), Token: m, Quote: usdc, QuoteSymbol: "USDC", Protocol: "pumpswap", Dex: "other", Created: true, Status: "active"}},
		// A: buy 1 SOL, sell 0.5 SOL; B: buy 30 USDC.
		Trades: []parse.Trade{tr(1, "A", "buy", "1000000000"), tr(2, "A", "sell", "500000000"), tr(3, "B", "buy", "30000000")},
	}
	apply(t, s, "solana", 1, m0.Add(10*time.Second), facts)
	// A trade 6 minutes later, beyond the 5-minute reach of the m0 SOL price.
	apply(t, s, "solana", 2, m0.Add(6*time.Minute+10*time.Second), parse.Facts{Trades: []parse.Trade{tr(1, "A", "buy", "2000000000")}})

	chains := []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}
	values, stats := compute.NewValues(s, chains), compute.NewStats(s, []string{"solana"})
	run := func() {
		t.Helper()
		if err := values.Step(ctx, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := stats.Step(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	row := func(pool string, minute time.Time) string {
		return one(t, c, `SELECT coalesce((SELECT concat_ws(' ', dex, trades, buys, sells, coalesce(trim_scale(volume_usd)::text, 'NULL'),
			coalesce(trim_scale(buy_volume_usd)::text, 'NULL'), coalesce(trim_scale(sell_volume_usd)::text, 'NULL'),
			coalesce(trim_scale(volume_native)::text, 'NULL'), unpriced_trades)
			FROM pool_minute_stats WHERE chain = 'solana' AND pool_id = $1 AND minute = $2), 'none')`, []byte(pool), minute)
	}
	check := func(when string, want map[string]string) {
		t.Helper()
		for k, w := range want {
			minute := m0
			if k[len(k)-1] == '1' {
				minute = m0.Add(6 * time.Minute)
			}
			if got := row(k[:1], minute); got != w {
				t.Errorf("%s: pool %s minute +%c = %s; want %s", when, k[:1], k[len(k)-1], got, w)
			}
		}
	}
	run()
	check("first pass", map[string]string{
		"A0": "pumpswap 2 1 1 300 200 100 1.5 0", // 1 SOL + 0.5 SOL at 200 USD
		"B0": "other 1 1 0 30 30 0 0.15 0",
		"A1": "pumpswap 1 1 0 NULL NULL 0 NULL 1", // the only buy is unpriced; no sells
	})
	// The SOL price for m0+6 arrives: the minute is recomputed after the fill.
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: m0.Add(6 * time.Minute), USD: "210", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	run()
	check("after the price fill", map[string]string{"A1": "pumpswap 1 1 0 420 420 0 2 0"})
	// Block 1 is reparsed with only B's trade: A's m0 row goes away.
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: 1, Time: m0.Add(10 * time.Second), Version: 2,
		Facts: parse.Facts{Trades: []parse.Trade{tr(3, "B", "buy", "30000000")}}}); err != nil {
		t.Fatal(err)
	}
	run()
	check("after reparse", map[string]string{"A0": "none", "B0": "other 1 1 0 30 30 0 0.15 0"})
}
