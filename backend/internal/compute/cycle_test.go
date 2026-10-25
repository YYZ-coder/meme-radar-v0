package compute_test

import (
	"context"
	"errors"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/prices"
	"github.com/meme-radar/radar/internal/store"
)

type downSource struct{}

func (downSource) Latest(context.Context, []string) (map[string]prices.Price, error) {
	return nil, errors.New("exchange down")
}
func (downSource) Minutes(context.Context, string, time.Time, time.Time) ([]prices.Price, error) {
	return nil, errors.New("exchange down")
}

func TestCycleRunsEveryStepAndSurvivesPriceOutage(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	now := day.Add(12 * time.Hour)
	wsol, m := []byte("wsol"), []byte("M")
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: wsol, Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	// An earlier price row: the outage only stops new prices.
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: now.Add(-time.Minute).Truncate(time.Minute), USD: "100", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	x := trade(1, []byte("A"), m, "1000000", "1000000000", "SOL")
	x.PriceNative, x.ValueNative = "1", "1"
	apply(t, s, "solana", 1, now.Add(-50*time.Second), parse.Facts{
		Tokens: []parse.Token{{Address: m, Created: true, Symbol: "M", Decimals: &six, TotalSupply: "1000000000"}},
		Pools:  []parse.Pool{{ID: []byte("A"), Token: m, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active", ReserveToken: "1", ReserveQuote: "5000000000"}},
		Trades: []parse.Trade{x},
	})
	cy := compute.NewCycle(compute.CycleConfig{Store: s, Prices: downSource{}, Chains: []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}, Lookback: 24 * time.Hour})
	priceErrs := testutil.ToFloat64(metrics.ComputeStepErrors.WithLabelValues("prices"))
	valuesRuns := histogramCount(t, "values")
	err := cy.Run(ctx, now)
	if got := testutil.ToFloat64(metrics.ComputeStepErrors.WithLabelValues("prices")) - priceErrs; got != 1 {
		t.Errorf("prices step errors reported = %v; want 1", got)
	}
	if got := histogramCount(t, "values") - valuesRuns; got != 1 {
		t.Errorf("values step durations reported = %v; want 1", got)
	}
	// Backlog gauges from the database after a full pass: one priced trade, nothing pending.
	for name, g := range map[string]float64{
		"trades":   testutil.ToFloat64(metrics.Trades24h.WithLabelValues("solana")),
		"unpriced": testutil.ToFloat64(metrics.UnpricedTrades.WithLabelValues("solana")) + 10,
		"unparsed": testutil.ToFloat64(metrics.UnparsedBlocks.WithLabelValues("solana")) + 10,
		"gaps":     testutil.ToFloat64(metrics.GapBlocks.WithLabelValues("solana")) + 10,
	} {
		if want := map[string]float64{"trades": 1, "unpriced": 10, "unparsed": 10, "gaps": 10}[name]; g != want {
			t.Errorf("%s gauge = %v; want %v", name, g, want)
		}
	}
	if err == nil {
		t.Errorf("Run = nil; want the price outage reported")
	}
	got := one(t, c, `SELECT concat_ws(' ', (SELECT trim_scale(value_usd) FROM trades), (SELECT trim_scale(liquidity_usd) FROM pool_market),
		(SELECT trim_scale(market_cap_usd) FROM token_market), (SELECT trades FROM pool_minute_stats), (SELECT count(*) FROM candles), (SELECT count(*) FROM coverage_ranges))`)
	// 1 SOL x 100; 10 SOL of liquidity; 1000 tokens x 100 USD; one stats row; 1m/5m/1h/1d candles; one covered range.
	if want := "100 1000 100000 1 4 1"; got != want {
		t.Errorf("after one cycle = %s; want %s", got, want)
	}
}

func TestIncrementalPassesRepriceOnlyTokensWithNewTrades(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	now := day.Add(12 * time.Hour)
	wsol, m := []byte("wsol"), []byte("M")
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: wsol, Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{latest: map[string]string{"SOL": "100"}}
	six := int16(6)
	tr := func(tx int, price string) parse.Trade {
		x := trade(tx, []byte("A"), m, "1000000", "1000000000", "SOL")
		x.PriceNative, x.ValueNative = price, price
		return x
	}
	apply(t, s, "solana", 1, now.Add(-5*time.Second), parse.Facts{
		Tokens: []parse.Token{{Address: m, Created: true, Symbol: "M", Decimals: &six, TotalSupply: "1000000000"}},
		Pools:  []parse.Pool{{ID: []byte("A"), Token: m, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active", ReserveToken: "1", ReserveQuote: "5000000000"}},
		Trades: []parse.Trade{tr(1, "1")},
	})
	cy := compute.NewCycle(compute.CycleConfig{Store: s, Prices: src, Chains: []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}, Lookback: 24 * time.Hour, FullEvery: time.Minute})
	if err := cy.Run(ctx, now); err != nil {
		t.Fatal(err)
	}
	// A new trade at 2 SOL: the next pass (not a full one) reprices M, but
	// pool liquidity is only refreshed by the next full pass.
	apply(t, s, "solana", 2, now.Add(5*time.Second), parse.Facts{Trades: []parse.Trade{tr(1, "2")},
		Pools: []parse.Pool{{ID: []byte("A"), Token: m, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Status: "active", ReserveToken: "1", ReserveQuote: "6000000000", Tx: 1, Ix: 1}}})
	state := func() string {
		return one(t, c, `SELECT concat_ws(' ', trim_scale(tm.price_usd), trim_scale(tm.market_cap_usd), trim_scale(pm.liquidity_usd))
			FROM token_market tm JOIN pool_market pm ON pm.chain = tm.chain AND pm.pool_id = tm.main_pool_id WHERE tm.address = 'M'`)
	}
	if err := cy.Run(ctx, now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, want := state(), "200 200000 1000"; got != want {
		t.Errorf("after an incremental pass = %s; want %s", got, want)
	}
	if err := cy.Run(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, want := state(), "200 200000 1200"; got != want {
		t.Errorf("after the next full pass = %s; want %s", got, want)
	}
}

func histogramCount(t *testing.T, step string) uint64 {
	t.Helper()
	var m dto.Metric
	if err := metrics.ComputeStepSeconds.WithLabelValues(step).(prometheus.Metric).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetHistogram().GetSampleCount()
}
