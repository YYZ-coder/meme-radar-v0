package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestTokenQuotedPoolsArePricedOneHop(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	now := day.Add(12 * time.Hour)
	wsol, q, m, z, r := []byte("wsol"), []byte("Q"), []byte("M"), []byte("Z"), []byte("R")
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: wsol, Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	var qp []store.QuotePrice
	for ago := 0; ago <= 4; ago++ {
		qp = append(qp, store.QuotePrice{Symbol: "SOL", Minute: now.Add(-time.Duration(ago) * time.Minute).Truncate(time.Minute), USD: "200", Source: "binance"})
	}
	if err := s.SaveQuotePrices(ctx, qp); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	tok := func(a []byte) parse.Token {
		return parse.Token{Address: a, Created: true, Symbol: string(a), Decimals: &six}
	}
	pool := func(id string, token, quote []byte, rq string, tx int) parse.Pool {
		p := parse.Pool{ID: []byte(id), Token: token, Quote: quote, QuoteSymbol: "", Protocol: "pumpswap", Dex: "d", Created: true, Status: "active", Tx: tx, Ix: 1}
		if rq != "" {
			p.ReserveToken, p.ReserveQuote = "1", rq
		}
		return p
	}
	tr := func(tx int, pool string, token []byte, tok, quote, price, value string) parse.Trade {
		x := trade(tx, []byte(pool), token, tok, quote, "")
		x.PriceNative, x.ValueNative = price, value
		return x
	}
	// Q trades against SOL at 0.5 SOL = 100 USD. M is quoted in Q; R is
	// quoted in Z, which is itself quoted in Q (two hops: not priced).
	apply(t, s, "solana", 1, now.Add(-3*time.Minute), parse.Facts{
		Tokens: []parse.Token{tok(q), tok(m), tok(z), tok(r)},
		Pools:  []parse.Pool{pool("PQ", q, wsol, "", 1), pool("PM", m, q, "10000000", 2), pool("PZ", z, q, "", 3), pool("PR", r, z, "", 4)},
		Trades: []parse.Trade{tr(1, "PQ", q, "1000000", "500000000", "0.5", "0.5"), tr(3, "PZ", z, "1000000", "1000000", "1", "")},
	})
	// 2 M for 4 Q; 1 R for 1 Z.
	apply(t, s, "solana", 2, now.Add(-1*time.Minute), parse.Facts{Trades: []parse.Trade{tr(2, "PM", m, "2000000", "4000000", "2", ""), tr(4, "PR", r, "1000000", "1000000", "1", "")}})

	chains := []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}
	values, market, liquidity := compute.NewValues(s, chains), compute.NewMarket(s, chains), compute.NewLiquidity(s, chains)
	for _, step := range []func() error{
		func() error { return values.Step(ctx, now.Add(-time.Hour)) },
		func() error { return market.Step(ctx, now, true) },
		func() error { return values.Step(ctx, now.Add(-time.Hour)) },
		func() error { return liquidity.Step(ctx, now) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	trade := func(pool string) string {
		return one(t, c, `SELECT concat_ws(' ', coalesce(trim_scale(value_usd)::text, 'NULL'), coalesce(trim_scale(value_native)::text, 'NULL'),
			coalesce(trim_scale(price_usd)::text, 'NULL')) FROM trades WHERE pool_id = $1`, []byte(pool))
	}
	for pool, want := range map[string]string{
		"PQ": "100 0.5 100",
		"PM": "400 2 200",      // 4 Q x 100 USD; 400 / 200 = 2 SOL; 2 Q per M = 200 USD
		"PZ": "100 0.5 100",    // Z is priced one hop through Q
		"PR": "NULL NULL NULL", // two hops
	} {
		if got := trade(pool); got != want {
			t.Errorf("trade in %s = %s; want %s", pool, got, want)
		}
	}
	// PM holds 10 Q: 20 Q of liquidity = 2000 USD = 10 SOL.
	if got := one(t, c, `SELECT concat_ws(' ', coalesce(trim_scale(liquidity_usd)::text, 'NULL'), coalesce(trim_scale(liquidity_native)::text, 'NULL')) FROM pool_market WHERE pool_id = 'PM'`); got != "2000 10" {
		t.Errorf("PM liquidity = %s; want 2000 10", got)
	}
}
