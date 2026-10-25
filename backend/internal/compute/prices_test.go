package compute_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/prices"
)

// fakeSource answers Latest with fixed prices and Minutes with minute-of-hour
// based closes, recording every Minutes request.
type fakeSource struct {
	latest map[string]string
	gaps   map[time.Time]bool // minutes the exchange has no candle for
	calls  []string
}

func (f *fakeSource) Latest(ctx context.Context, symbols []string) (map[string]prices.Price, error) {
	out := map[string]prices.Price{}
	for _, s := range symbols {
		out[s] = prices.Price{Symbol: s, USD: f.latest[s], Source: "binance"}
	}
	return out, nil
}

func (f *fakeSource) Minutes(ctx context.Context, symbol string, from, to time.Time) ([]prices.Price, error) {
	f.calls = append(f.calls, fmt.Sprintf("%s %s-%s", symbol, from.Format("15:04"), to.Format("15:04")))
	var out []prices.Price
	for m := from; !m.After(to); m = m.Add(time.Minute) {
		if !f.gaps[m] {
			out = append(out, prices.Price{Symbol: symbol, T: m, USD: fmt.Sprintf("%d.5", 100+m.Minute()), Source: "coinbase"})
		}
	}
	return out, nil
}

func TestPricesStepRecordsLiveAndFillsMissingMinutes(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	m0 := day.Add(10 * time.Hour)
	pool, tok := []byte("pool"), []byte("token")
	solTrades := func(n uint64, at time.Time) {
		apply(t, s, "solana", n, at, parse.Facts{Trades: []parse.Trade{trade(0, pool, tok, "1", "1", "SOL")}})
	}
	solTrades(10, m0.Add(1*time.Minute+5*time.Second))
	solTrades(11, m0.Add(2*time.Minute+5*time.Second))
	solTrades(12, m0.Add(4*time.Minute+5*time.Second)) // no candle: exchange gap
	solTrades(13, m0.Add(9*time.Minute+5*time.Second)) // current minute: live price only
	apply(t, s, "bsc", 50, m0.Add(3*time.Minute), parse.Facts{Trades: []parse.Trade{trade(0, pool, tok, "1", "1", "BNB")}})

	src := &fakeSource{latest: map[string]string{"SOL": "150.25", "BNB": "600"}, gaps: map[time.Time]bool{m0.Add(4 * time.Minute): true}}
	now := m0.Add(9*time.Minute + 30*time.Second)
	p := compute.NewPrices(s, src, []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}, {Chain: "bsc", Symbol: "BNB"}}, 24*time.Hour)
	if err := p.Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	price := func(sym string, m time.Time) string {
		return one(t, c, `SELECT coalesce((SELECT price_usd::text || ' ' || source FROM quote_prices WHERE symbol = $1 AND minute = $2), 'none')`, sym, m)
	}
	for _, w := range []struct {
		sym  string
		m    time.Time
		want string
	}{
		{"SOL", m0.Add(9 * time.Minute), "150.25 binance"},
		{"BNB", m0.Add(9 * time.Minute), "600 binance"},
		{"SOL", m0.Add(1 * time.Minute), "101.5 coinbase"},
		{"SOL", m0.Add(2 * time.Minute), "102.5 coinbase"},
		{"SOL", m0.Add(4 * time.Minute), "none"},
		{"BNB", m0.Add(3 * time.Minute), "103.5 coinbase"},
	} {
		if got := price(w.sym, w.m); got != w.want {
			t.Errorf("%s %s = %s; want %s", w.sym, w.m.Format("15:04"), got, w.want)
		}
	}
	// Contiguous missing minutes go in one request; the current minute is live-only.
	if want := "[SOL 10:01-10:02 SOL 10:04-10:04 BNB 10:03-10:03]"; fmt.Sprint(src.calls) != want {
		t.Errorf("minute requests = %v; want %s", src.calls, want)
	}
	// A minute the exchange has no candle for is not asked again right away.
	src.calls = nil
	if err := p.Step(ctx, now.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(src.calls) != 0 {
		t.Errorf("second step requests = %v; want none", src.calls)
	}
}
