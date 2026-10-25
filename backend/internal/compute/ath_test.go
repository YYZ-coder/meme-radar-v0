package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestATHTracksHighestCandle(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	t0 := day.Add(10 * time.Hour)
	chains := []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: []byte("wsol"), Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	var prices []store.QuotePrice
	for i := 0; i < 5; i++ {
		prices = append(prices, store.QuotePrice{Symbol: "SOL", Minute: t0.Add(time.Duration(i) * time.Minute), USD: "100", Source: "binance"})
	}
	if err := s.SaveQuotePrices(ctx, prices); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	add := func(n uint64, quote, price string) {
		tr := trade(1, []byte("A"), []byte("M"), "1000000", quote, "SOL")
		tr.PriceNative = price
		tr.ValueNative = price
		f := parse.Facts{Trades: []parse.Trade{tr}}
		if n == 1 {
			f.Tokens = []parse.Token{{Address: []byte("M"), Created: true, Decimals: &six, TotalSupply: "1000000000000000"}}
			f.Pools = []parse.Pool{{ID: []byte("A"), Token: []byte("M"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"}}
		}
		apply(t, s, "solana", n, t0.Add(time.Duration(n-1)*time.Minute+5*time.Second), f)
	}
	add(1, "10000000", "0.01")
	add(2, "30000000", "0.03")
	add(3, "20000000", "0.02")
	at := t0.Add(3 * time.Minute)
	if err := s.SaveMainPools(ctx, "solana", []store.MainPoolChange{{Token: []byte("M"), Pool: []byte("A"), Reason: "liquidity"}}, t0); err != nil {
		t.Fatal(err)
	}
	refresh := func(now time.Time) {
		t.Helper()
		if err := compute.NewValues(s, chains).Step(ctx, t0); err != nil {
			t.Fatal(err)
		}
		if err := compute.NewMarket(s, chains).Step(ctx, now, true); err != nil {
			t.Fatal(err)
		}
		if err := compute.NewStats(s, []string{"solana"}).Step(ctx, now); err != nil {
			t.Fatal(err)
		}
		if err := compute.NewATH(s, []string{"solana"}).Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	check := func(price, cap string, minute int) {
		t.Helper()
		got := one(t, c, `SELECT coalesce(trim_scale(ath_price_usd)::text,'NULL')||' '||coalesce(trim_scale(ath_market_cap_usd)::text,'NULL')||' '||coalesce(extract(epoch FROM(ath_at-$1::timestamptz))::integer::text,'NULL') FROM token_market WHERE chain='solana' AND address='M'`, t0)
		want := price + " " + cap + " " + map[int]string{1: "60", 4: "240"}[minute]
		if got != want {
			t.Errorf("ATH=%s; want %s (independent candle high x 1e9)", got, want)
		}
	}
	refresh(at)
	check("3", "3000000000", 1)
	add(4, "25000000", "0.025")
	refresh(t0.Add(4 * time.Minute))
	check("3", "3000000000", 1)
	add(5, "40000000", "0.04")
	refresh(t0.Add(5 * time.Minute))
	check("4", "4000000000", 4)
	// Retention removes 1m after 30d, but keeps 1d.
	if err := s.PruneCompute(ctx, t0.Add(35*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Exercise the persisted high after pruning too: equal/lower source highs
	// must not replace its original 1m timestamp with the daily bucket start.
	if err := compute.NewATH(s, []string{"solana"}).Step(ctx); err != nil {
		t.Fatal(err)
	}
	check("4", "4000000000", 4)
}

func TestATHRunsAfterStatsOnFullCycles(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	now := day.Add(10*time.Hour + 30*time.Second)
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: []byte("wsol"), Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	tr := trade(1, []byte("A"), []byte("M"), "1000000", "30000000", "SOL")
	tr.PriceNative = "0.03"
	tr.ValueNative = "0.03"
	apply(t, s, "solana", 1, now.Add(-5*time.Second), parse.Facts{
		Tokens: []parse.Token{{Address: []byte("M"), Created: true, Decimals: &six, TotalSupply: "1000000000000000"}},
		Pools:  []parse.Pool{{ID: []byte("A"), Token: []byte("M"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"}},
		Trades: []parse.Trade{tr},
	})
	cy := compute.NewCycle(compute.CycleConfig{Store: s, Prices: &fakeSource{latest: map[string]string{"SOL": "100"}}, Chains: []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}, Lookback: 24 * time.Hour, FullEvery: time.Minute})
	before := histogramCount(t, "ath")
	state := func() string {
		return one(t, c, `SELECT concat_ws(' ',coalesce(trim_scale(ath_price_usd)::text,'NULL'),coalesce(trim_scale(ath_market_cap_usd)::text,'NULL')) FROM token_market WHERE chain='solana' AND address='M'`)
	}
	if err := cy.Run(ctx, now); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != "3 3000000000" {
		t.Errorf("first full cycle ATH=%s; want 3 3000000000 after its Stats step", got)
	}
	if got := histogramCount(t, "ath") - before; got != 1 {
		t.Errorf("ATH duration samples=%d; want 1", got)
	}
	tr.QuoteAmount = "40000000"
	tr.PriceNative = "0.04"
	tr.ValueNative = "0.04"
	apply(t, s, "solana", 2, now.Add(5*time.Second), parse.Facts{Trades: []parse.Trade{tr}})
	if err := cy.Run(ctx, now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != "3 3000000000" {
		t.Errorf("incremental ATH=%s; want previous 3 3000000000", got)
	}
	if got := histogramCount(t, "ath") - before; got != 1 {
		t.Errorf("ATH samples after incremental=%d; want unchanged 1", got)
	}
	if err := cy.Run(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := state(); got != "4 4000000000" {
		t.Errorf("next full cycle ATH=%s; want 4 4000000000", got)
	}
	if got := histogramCount(t, "ath") - before; got != 2 {
		t.Errorf("ATH samples after full=%d; want 2", got)
	}
}

// Regression supplement: the daily-candle path already exists in H1-8. This
// independently starts with NULL ATH after pruning, so keeping an old high
// cannot hide a failure to read the retained daily candle.
func TestATHUsesRetainedDailyCandle(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	now := day.Add(10 * time.Hour)
	chains := []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: []byte("wsol"), Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: now, USD: "100", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	tr := trade(1, []byte("A"), []byte("M"), "1000000", "70000000", "SOL")
	tr.PriceNative = "0.07"
	tr.ValueNative = "0.07"
	apply(t, s, "solana", 1, now.Add(5*time.Second), parse.Facts{
		Tokens: []parse.Token{{Address: []byte("M"), Created: true, Decimals: &six, TotalSupply: "1000000000000000"}},
		Pools:  []parse.Pool{{ID: []byte("A"), Token: []byte("M"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"}},
		Trades: []parse.Trade{tr},
	})
	if err := compute.NewValues(s, chains).Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := compute.NewMarket(s, chains).Step(ctx, now.Add(time.Minute), true); err != nil {
		t.Fatal(err)
	}
	if err := compute.NewStats(s, []string{"solana"}).Step(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneCompute(ctx, now.Add(35*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM candles WHERE interval='1m'`); got != "0" {
		t.Fatalf("1m rows after pruning=%s; want 0", got)
	}
	if err := compute.NewATH(s, []string{"solana"}).Step(ctx); err != nil {
		t.Fatal(err)
	}
	got := one(t, c, `SELECT concat_ws(' ',trim_scale(ath_price_usd),trim_scale(ath_market_cap_usd),extract(epoch FROM(ath_at-$1::timestamptz))::integer) FROM token_market WHERE chain='solana' AND address='M'`, day)
	if got != "7 7000000000 0" {
		t.Errorf("daily-only ATH=%s; want 7 7000000000 at daily bucket UTC midnight", got)
	}
}
