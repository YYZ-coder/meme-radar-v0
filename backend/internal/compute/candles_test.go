package compute_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestCandlesFollowTheMainPoolAndRollUp(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	m0 := day.Add(10 * time.Hour)
	now := m0.Add(8 * time.Minute)
	wsol, m := []byte("wsol"), []byte("M")
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: wsol, Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	var qp []store.QuotePrice
	for i := 0; i <= 8; i++ {
		qp = append(qp, store.QuotePrice{Symbol: "SOL", Minute: m0.Add(time.Duration(i) * time.Minute), USD: "100", Source: "binance"})
	}
	if err := s.SaveQuotePrices(ctx, qp); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	// tr trades 1 token at price SOL per token (value = price SOL).
	tr := func(tx int, pool, price string) parse.Trade {
		x := trade(tx, []byte(pool), m, "1000000", "", "SOL")
		x.PriceNative, x.ValueNative = price, price
		x.QuoteAmount = strings.TrimLeft(strings.ReplaceAll(price, ".", "")+strings.Repeat("0", 9-len(price[strings.Index(price, ".")+1:])), "0")
		return x
	}
	pool := func(id, rq string) parse.Pool {
		return parse.Pool{ID: []byte(id), Token: m, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active", ReserveToken: "1", ReserveQuote: rq}
	}
	// A is the main pool (100 SOL vs 1 SOL); B's trade only adds volume.
	apply(t, s, "solana", 1, m0.Add(5*time.Second), parse.Facts{
		Tokens: []parse.Token{{Address: m, Created: true, Symbol: "M", Decimals: &six}},
		Pools:  []parse.Pool{pool("A", "100000000000"), pool("B", "1000000000")},
		Trades: []parse.Trade{tr(1, "A", "0.01"), tr(2, "A", "0.015"), tr(3, "B", "0.09"), tr(4, "A", "0.008")},
	})
	apply(t, s, "solana", 2, m0.Add(40*time.Second), parse.Facts{Trades: []parse.Trade{tr(1, "A", "0.012")}})
	apply(t, s, "solana", 3, m0.Add(70*time.Second), parse.Facts{Trades: []parse.Trade{tr(1, "A", "0.013")}})
	apply(t, s, "solana", 4, m0.Add(6*time.Minute+1*time.Second), parse.Facts{Trades: []parse.Trade{tr(1, "A", "0.02")}})

	chains := []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}
	for _, step := range []func() error{
		func() error { return compute.NewValues(s, chains).Step(ctx, now.Add(-time.Hour)) },
		func() error { return compute.NewLiquidity(s, chains).Step(ctx, now) },
		func() error { return compute.NewMarket(s, chains).Step(ctx, now, true) },
		func() error { return compute.NewStats(s, []string{"solana"}).Step(ctx, now) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	got := one(t, c, `SELECT string_agg(concat_ws(' ', interval, to_char(t AT TIME ZONE 'UTC', 'HH24:MI'), pool_id::text,
		trim_scale(o), trim_scale(h), trim_scale(l), trim_scale(c), trim_scale(c_native), trim_scale(v), closed::text), E'\n' ORDER BY interval, t)
		FROM candles WHERE chain = 'solana' AND token_address = 'M'`)
	// USD = SOL price x 100. Minute 10:00: A 1, 1.5, 0.8, 1.2 (B's 9 only in v: 1+1.5+9+0.8+1.2 = 13.5).
	want := strings.Join([]string{
		`1d 00:00 \x41 1 2 0.8 2 0.02 16.8 false`,
		`1h 10:00 \x41 1 2 0.8 2 0.02 16.8 false`,
		`1m 10:00 \x41 1 1.5 0.8 1.2 0.012 13.5 true`,
		`1m 10:01 \x41 1.3 1.3 1.3 1.3 0.013 1.3 true`,
		`1m 10:06 \x41 2 2 2 2 0.02 2 true`,
		`5m 10:00 \x41 1 1.5 0.8 1.3 0.013 14.8 true`,
		`5m 10:05 \x41 2 2 2 2 0.02 2 false`,
	}, "\n")
	if got != want {
		t.Errorf("candles:\n%s\nwant:\n%s", got, want)
	}
	// A later trade in the same 5-minute bucket rebuilds the existing 5m/1h/1d candles.
	apply(t, s, "solana", 5, m0.Add(7*time.Minute), parse.Facts{Trades: []parse.Trade{tr(1, "A", "0.03")}})
	if err := compute.NewValues(s, chains).Step(ctx, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := compute.NewStats(s, []string{"solana"}).Step(ctx, now); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if got := one(t, c, `SELECT trim_scale(h) || ' ' || trim_scale(c) || ' ' || trim_scale(v) FROM candles WHERE token_address = 'M' AND interval = '5m' AND t = $1`, m0.Add(5*time.Minute)); got != "3 3 5" {
		t.Errorf("5m 10:05 after a new trade = %s; want 3 3 5", got)
	}
}

func TestCandlesUseMainPoolPerTrade(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	t0 := day.Add(10 * time.Hour)
	m, wsol := []byte("M"), []byte("wsol")
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: wsol, Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: t0, USD: "100", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	for i, tr := range []struct {
		second             int
		pool, quote, price string
	}{
		{10, "A", "10000000", "0.01"}, {20, "A", "12000000", "0.012"}, {40, "B", "20000000", "0.02"}, {45, "A", "90000000", "0.09"}, {50, "B", "22000000", "0.022"},
	} {
		x := trade(1, []byte(tr.pool), m, "1000000", tr.quote, "SOL")
		x.PriceNative = tr.price
		x.ValueNative = tr.price
		facts := parse.Facts{Trades: []parse.Trade{x}}
		if i == 0 {
			facts.Tokens = []parse.Token{{Address: m, Created: true, Decimals: &six}}
			facts.Pools = []parse.Pool{{ID: []byte("A"), Token: m, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"}, {ID: []byte("B"), Token: m, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"}}
		}
		apply(t, s, "solana", uint64(i+1), t0.Add(time.Duration(tr.second)*time.Second), facts)
	}
	if err := s.SaveMainPools(ctx, "solana", []store.MainPoolChange{{Token: m, Pool: []byte("A"), Reason: "liquidity"}}, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMainPools(ctx, "solana", []store.MainPoolChange{{Token: m, From: []byte("A"), Pool: []byte("B"), Reason: "liquidity"}}, t0.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		at   time.Time
		pool string
	}{{t0.Add(29 * time.Second), "41"}, {t0.Add(30 * time.Second), "42"}, {t0.Add(-time.Minute), "41"}} {
		if got := one(t, c, `SELECT encode(main_pool_at('solana',$1,$2),'hex')`, m, tc.at); got != tc.pool {
			t.Errorf("main_pool_at(%s)=%s; want %s", tc.at, got, tc.pool)
		}
	}
	if err := compute.NewValues(s, []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}).Step(ctx, t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := compute.NewStats(s, []string{"solana"}).Step(ctx, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := one(t, c, `SELECT concat_ws(' ',trim_scale(o),trim_scale(h),trim_scale(l),trim_scale(c),trim_scale(o_native),trim_scale(h_native),trim_scale(l_native),trim_scale(c_native),encode(pool_id,'hex'),trim_scale(v)) FROM candles WHERE chain='solana' AND token_address=$1 AND interval='1m' AND t=$2`, m, t0)
	// Independent H1-9 example: pre-switch A prices 1,1.2; post-switch B 2,2.2.
	// A's post-switch 9 contributes volume only: 1+1.2+2+9+2.2=15.4.
	if want := "1 2.2 1 2.2 0.01 0.022 0.01 0.022 42 15.4"; got != want {
		t.Errorf("per-trade main-pool candle=%s; want %s", got, want)
	}
}

// H1-12 exercises the persisted candle boundary and the public read API.
// Expectations are the task book's independent sums, not sums of stored bars.
func TestCoarseCandleVolumeIncludesMinutesWithoutBars(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	t0 := day.Add(10 * time.Hour)
	now := t0.Add(7 * time.Minute)
	token, wsol := []byte("M"), []byte("wsol")
	chains := []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: wsol, Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	var prices []store.QuotePrice
	for minute := 0; minute <= 7; minute++ {
		prices = append(prices, store.QuotePrice{Symbol: "SOL", Minute: t0.Add(time.Duration(minute) * time.Minute), USD: "100", Source: "binance"})
	}
	if err := s.SaveQuotePrices(ctx, prices); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	for _, x := range []struct {
		num                 uint64
		minute              int
		pool, quote, native string
	}{
		{1, 0, "A", "1500000000", "1.5"}, // 1.5 SOL * 100 = 150 USD.
		{2, 1, "B", "2000000000", "2"},   // B alone: 200 USD but no main-pool bar.
		{4, 6, "A", "500000000", "0.5"},  // After 10:05: 50 USD.
	} {
		tr := trade(1, []byte(x.pool), token, "1000000", x.quote, "SOL")
		tr.PriceNative, tr.ValueNative = x.native, x.native
		f := parse.Facts{Trades: []parse.Trade{tr}}
		if x.num == 1 {
			f.Tokens = []parse.Token{{Address: token, Created: true, Decimals: &six}}
			f.Pools = []parse.Pool{
				{ID: []byte("A"), Token: token, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"},
				{ID: []byte("B"), Token: token, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"},
			}
		}
		apply(t, s, "solana", x.num, t0.Add(time.Duration(x.minute)*time.Minute+5*time.Second), f)
	}
	if err := s.SaveMainPools(ctx, "solana", []store.MainPoolChange{{Token: token, Pool: []byte("A"), Reason: "liquidity"}}, t0); err != nil {
		t.Fatal(err)
	}
	refresh := func() {
		t.Helper()
		if err := compute.NewValues(s, chains).Step(ctx, t0); err != nil {
			t.Fatal(err)
		}
		if err := compute.NewStats(s, []string{"solana"}).Step(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	check := func(five, hour, dayVolume string) {
		t.Helper()
		for _, x := range []struct {
			interval     string
			at, to       time.Time
			volume, ohlc string
		}{
			{"5m", t0, t0.Add(5 * time.Minute), five, "150 150 150 150"},
			{"1h", t0, t0.Add(time.Hour), hour, "150 150 50 50"},
			{"1d", day, day.Add(24 * time.Hour), dayVolume, "150 150 50 50"},
		} {
			got := one(t, c, `SELECT trim_scale(v)::text FROM candles WHERE chain='solana' AND token_address=$1 AND interval=$2 AND t=$3`, token, x.interval, x.at)
			if got != x.volume {
				t.Errorf("%s volume=%s; want %s including all priced pool-minutes", x.interval, got, x.volume)
			}
			got = one(t, c, `SELECT concat_ws(' ',trim_scale(o),trim_scale(h),trim_scale(l),trim_scale(c)) FROM candles WHERE chain='solana' AND token_address=$1 AND interval=$2 AND t=$3`, token, x.interval, x.at)
			if got != x.ohlc {
				t.Errorf("%s OHLC=%s; want main-pool-only %s", x.interval, got, x.ohlc)
			}
			for _, native := range []bool{false, true} {
				set, err := s.TokenCandles(ctx, "solana", token, x.interval, "price", native, x.at, x.to, 10, now)
				if err != nil {
					t.Fatal(err)
				}
				if len(set.Bars) != 1 {
					t.Fatalf("%s native=%t bars=%d; want one", x.interval, native, len(set.Bars))
				}
				v := set.Bars[0].V
				if v == nil || *v != x.volume {
					t.Errorf("%s native=%t returned v=%v; want USD %s", x.interval, native, v, x.volume)
				}
			}
		}
		if got := one(t, c, `SELECT count(*)::text FROM candles WHERE chain='solana' AND token_address=$1 AND interval='1m' AND t IN($2,$3)`, token, t0.Add(time.Minute), t0.Add(2*time.Minute)); got != "0" {
			t.Errorf("non-main-only minute bars=%s; want 0", got)
		}
		for _, x := range []struct {
			at     time.Time
			volume string
		}{{t0, "150"}, {t0.Add(6 * time.Minute), "50"}} {
			if got := one(t, c, `SELECT trim_scale(v)::text FROM candles WHERE chain='solana' AND token_address=$1 AND interval='1m' AND t=$2`, token, x.at); got != x.volume {
				t.Errorf("1m %s volume=%s; want unchanged %s", x.at, got, x.volume)
			}
		}
	}
	refresh()
	// Independent sums: 5m 150+200=350; 1h/1d 150+200+50=400.
	check("350", "400", "400")
	// Late block 3 fills a historical minute with B alone (25 USD). Rebuilding
	// that minute must update its containing coarse buckets even without a 1m
	// bar, and must read the freshly rebuilt pool_minute_stats in the same txn.
	tr := trade(1, []byte("B"), token, "1000000", "250000000", "SOL")
	tr.PriceNative, tr.ValueNative = "0.25", "0.25"
	apply(t, s, "solana", 3, t0.Add(2*time.Minute+5*time.Second), parse.Facts{Trades: []parse.Trade{tr}})
	refresh()
	check("375", "425", "425") // 350+25; 400+25.
}
