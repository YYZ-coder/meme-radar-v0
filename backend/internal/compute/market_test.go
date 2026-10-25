package compute_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestMarketStepSetsMainPoolAndTokenState(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	now := day.Add(12 * time.Hour)
	wsol, m, n := []byte("wsol"), []byte("M"), []byte("N")
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: wsol, Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	var qp []store.QuotePrice
	for _, ago := range []int{20, 5, 0} {
		qp = append(qp, store.QuotePrice{Symbol: "SOL", Minute: now.Add(-time.Duration(ago) * time.Minute).Truncate(time.Minute), USD: "200", Source: "binance"})
	}
	if err := s.SaveQuotePrices(ctx, qp); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	pool := func(id string, token []byte, protocol, rq string, tx int) parse.Pool {
		p := parse.Pool{ID: []byte(id), Token: token, Quote: wsol, QuoteSymbol: "SOL", Protocol: protocol, Dex: "d", Created: true, Status: "active", Tx: tx, Ix: 1}
		if rq != "" {
			p.ReserveToken, p.ReserveQuote = "1", rq
		}
		return p
	}
	tr := func(tx int, pool string, token []byte, tok, quote, price, value string) parse.Trade {
		x := trade(tx, []byte(pool), token, tok, quote, "SOL")
		x.PriceNative, x.ValueNative = price, value
		return x
	}
	// M: 1e9 tokens; curve P1 holds 10 SOL, AMM P2 50 SOL. N trades only on a v4 pool.
	apply(t, s, "solana", 1, now.Add(-20*time.Minute), parse.Facts{
		Tokens: []parse.Token{{Address: m, Created: true, Symbol: "M", Decimals: &six, TotalSupply: "1000000000000000"},
			{Address: n, Created: true, Symbol: "N", Decimals: &six}},
		Pools:  []parse.Pool{pool("P1", m, "pump_curve", "10000000000", 1), pool("P2", m, "pumpswap", "50000000000", 2), pool("V4", n, "uniswap_v4", "", 3)},
		Trades: []parse.Trade{tr(1, "P1", m, "100000000", "2000000000", "0.00002", "2"), tr(3, "V4", n, "1000000", "1000000000", "1", "1")},
	})
	apply(t, s, "solana", 2, now.Add(-5*time.Minute), parse.Facts{Trades: []parse.Trade{tr(1, "P2", m, "100000000000", "3000000000", "0.00003", "3")}})

	chains := []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}
	if err := compute.NewValues(s, chains).Step(ctx, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := compute.NewLiquidity(s, chains).Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := compute.NewMarket(s, chains).Step(ctx, now, true); err != nil {
		t.Fatal(err)
	}
	state := func(token []byte) string {
		return one(t, c, `SELECT concat_ws(' ', main_pool_id::text, coalesce(trim_scale(price_usd)::text, 'NULL'), coalesce(trim_scale(price_native)::text, 'NULL'),
			coalesce(trim_scale(market_cap_usd)::text, 'NULL'), coalesce(trim_scale(market_cap_native)::text, 'NULL'),
			coalesce(trim_scale(liquidity_usd)::text, 'NULL'), coalesce(trim_scale(liquidity_native)::text, 'NULL'), to_char(last_trade_at, 'HH24:MI'))
			FROM token_market WHERE chain = 'solana' AND address = $1`, token)
	}
	// M: main pool P2 (20000 USD vs 4000 USD); price 0.00003 SOL = 0.006 USD; market cap x 1e9; liquidity 4000 + 20000 USD.
	if got, want := state(m), `\x5032 0.006 0.00003 6000000 30000 24000 120 11:55`; got != want {
		t.Errorf("token M = %s; want %s", got, want)
	}
	// N: the v4 pool is the only one; its liquidity is unknown, so is N's; no supply, no market cap.
	if got, want := state(n), `\x5634 200 1 NULL NULL NULL NULL 11:40`; got != want {
		t.Errorf("token N = %s; want %s", got, want)
	}
	if got := one(t, c, `SELECT count(*)::text || ' ' || string_agg(coalesce(from_pool::text, '-') || '>' || to_pool::text || ' ' || reason, ',') FROM pool_switches WHERE token_address = $1`, m); got != `1 ->\x5032 liquidity` {
		t.Errorf("M pool switches = %s; want one initial switch to P2", got)
	}
}

func TestGraduationSwitchUsesChainTime(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		created, hasTrade, precomputed bool
		second                         int
	}{
		{"pool_creation", true, true, false, 5}, {"first_trade", false, true, false, 20}, {"discovery_fallback", false, false, false, 60}, {"rebuild_already_computed", true, true, true, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, c := open(t)
			t0 := day.Add(10 * time.Hour)
			m, wsol, p1, p2 := []byte("M"), []byte("wsol"), []byte("P1"), []byte("P2")
			six := int16(6)
			if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: wsol, Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: t0.Add(-time.Minute), USD: "100", Source: "binance"}, {Symbol: "SOL", Minute: t0, USD: "100", Source: "binance"}}); err != nil {
				t.Fatal(err)
			}
			initial := trade(1, p1, m, "1000000", "10000000", "SOL")
			initial.PriceNative = "0.01"
			initial.ValueNative = "0.01"
			apply(t, s, "solana", 1, t0.Add(-30*time.Second), parse.Facts{Tokens: []parse.Token{{Address: m, Created: true, Decimals: &six}}, Pools: []parse.Pool{{ID: p1, Token: m, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pump_curve", Dex: "pump_fun", Created: true, Status: "active", ReserveToken: "1000000", ReserveQuote: "1000000000"}}, Trades: []parse.Trade{initial}})
			if err := s.SaveMainPools(ctx, "solana", []store.MainPoolChange{{Token: m, Pool: p1, Reason: "liquidity"}}, t0.Add(-30*time.Second)); err != nil {
				t.Fatal(err)
			}
			apply(t, s, "solana", 2, t0.Add(5*time.Second), parse.Facts{Pools: []parse.Pool{
				{ID: p1, Token: m, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pump_curve", Dex: "pump_fun", Status: "graduated", GraduatedTo: p2},
				{ID: p2, Token: m, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: tc.created, Status: "active", ReserveToken: "1000000", ReserveQuote: "2000000000"},
			}})
			if tc.hasTrade {
				x := trade(1, p2, m, "1000000", "20000000", "SOL")
				x.PriceNative = "0.02"
				x.ValueNative = "0.02"
				apply(t, s, "solana", 3, t0.Add(20*time.Second), parse.Facts{Trades: []parse.Trade{x}})
			}
			chains := []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}
			if err := compute.NewValues(s, chains).Step(ctx, t0.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			if err := compute.NewLiquidity(s, chains).Step(ctx, t0.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if tc.precomputed {
				if err := compute.NewStats(s, []string{"solana"}).Step(ctx, t0.Add(30*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			if err := compute.NewMarket(s, chains).Step(ctx, t0.Add(time.Minute), true); err != nil {
				t.Fatal(err)
			}
			got := one(t, c, `SELECT extract(epoch FROM (t-$1::timestamptz))::integer::text||' '||reason FROM pool_switches WHERE chain='solana' AND token_address=$2 AND from_pool=$3 AND to_pool=$4`, t0, m, p1, p2)
			want := fmt.Sprintf("%d graduation", tc.second)
			if got != want {
				t.Errorf("graduation switch=%s; want chain-time %s", got, want)
			}
			if err := compute.NewStats(s, []string{"solana"}).Step(ctx, t0.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if tc.hasTrade {
				if got := one(t, c, `SELECT trim_scale(c)::text FROM candles WHERE chain='solana' AND token_address=$1 AND interval='1m' AND t=$2`, m, t0); got != "2" {
					t.Errorf("new-pool trade before discovery: candle close=%s; want 2", got)
				}
			}
		})
	}
}

func TestGraduationSwitchClampsToPreviousTime(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	t0 := day.Add(10 * time.Hour)
	m, p1, p2 := []byte("M"), []byte("P1"), []byte("P2")
	six := int16(6)
	apply(t, s, "solana", 1, t0.Add(5*time.Second), parse.Facts{Tokens: []parse.Token{{Address: m, Created: true, Decimals: &six}}, Pools: []parse.Pool{
		{ID: p1, Token: m, Quote: []byte("wsol"), QuoteSymbol: "SOL", Protocol: "pump_curve", Dex: "pump_fun", Created: true, Status: "active"},
		{ID: p2, Token: m, Quote: []byte("wsol"), QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"},
	}})
	previous := t0.Add(30 * time.Second)
	if err := s.SaveMainPools(ctx, "solana", []store.MainPoolChange{{Token: m, Pool: p1, Reason: "liquidity"}}, previous); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMainPools(ctx, "solana", []store.MainPoolChange{{Token: m, From: p1, Pool: p2, Reason: "graduation"}}, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT coalesce((SELECT extract(epoch FROM (t-$1::timestamptz))::integer::text FROM pool_switches WHERE chain='solana' AND token_address=$2 AND to_pool=$3 ORDER BY t DESC LIMIT 1),'missing')`, t0, m, p2); got != "30" {
		t.Errorf("clamped graduation time=%s; want 30 (max of previous 30 and creation 5)", got)
	}
	if got := one(t, c, `SELECT count(*)::text FROM pool_switches WHERE chain='solana' AND token_address=$1 AND t=$2`, m, previous); got != "2" {
		t.Errorf("switches at the clamped time=%s; want 2", got)
	}
	if got := one(t, c, `SELECT string_agg(encode(to_pool,'hex'),',' ORDER BY t,seq) FROM pool_switches WHERE chain='solana' AND token_address=$1`, m); got != "5031,5032" {
		t.Errorf("ordered tied switches=%s; want 5031,5032", got)
	}
	set, err := s.TokenCandles(ctx, "solana", m, "1m", "price", false, t0, t0.Add(time.Minute), 10, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Switches) != 2 || string(set.Switches[0].To) != "P1" || string(set.Switches[1].To) != "P2" || set.Switches[0].Seq >= set.Switches[1].Seq {
		t.Errorf("returned tied switches=%+v; want P1 then P2 in seq order", set.Switches)
	}
	for _, tc := range []struct {
		at   time.Time
		want string
	}{{previous.Add(-time.Microsecond), "5031"}, {previous, "5032"}} {
		if got := one(t, c, `SELECT encode(main_pool_at('solana',$1,$2),'hex')`, m, tc.at); got != tc.want {
			t.Errorf("main_pool_at(%s)=%s; want %s (retain first allocation before the tied switch)", tc.at, got, tc.want)
		}
	}
}
