package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

// parsedAt commits block num at t and writes one trade per given second
// offset (and one liquidity event when liq), the way parse would.
func parsedAt(t *testing.T, s *store.Store, chain string, num uint64, at time.Time, liq bool, secs ...int) {
	t.Helper()
	ctx := context.Background()
	b := block(num, store.StatusConfirmed)
	b.Time = at
	if err := s.Commit(ctx, store.Commit{Chain: chain, Block: b}); err != nil {
		t.Fatal(err)
	}
	f := parse.Facts{Pools: []parse.Pool{{ID: curve, Token: mint, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pump_curve", Dex: "pump_fun", Created: true, Status: "active"}}}
	for i := range secs {
		f.Trades = append(f.Trades, parse.Trade{Tx: i, Ix: 1, TxHash: []byte{byte(i)}, Pool: curve, Token: mint, Wallet: user, Attribution: "confirmed",
			Side: "buy", TokenAmount: "1", QuoteAmount: "1", QuoteSymbol: "SOL", Decoder: "pump_fun", Version: 1})
	}
	if liq {
		f.Liquidity = []parse.Liquidity{{Tx: 50, Ix: 1, TxHash: []byte{50}, Pool: curve, Token: mint, Kind: "add", TokenAmount: "1", QuoteAmount: "1", QuoteSymbol: "SOL", Decoder: "pumpswap", Version: 1}}
	}
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: chain, Num: num, Time: at, Version: 1, Facts: f}); err != nil {
		t.Fatal(err)
	}
}

func TestQuotePricesUpsertAndMissingMinutes(t *testing.T) {
	ctx := context.Background()
	s, c := openWithConn(t)
	m0 := day.Add(10 * time.Hour)
	parsedAt(t, s, "solana", 100, m0.Add(5*time.Second), false, 0)
	parsedAt(t, s, "solana", 101, m0.Add(70*time.Second), false, 0, 0)
	parsedAt(t, s, "solana", 102, m0.Add(3*time.Minute+59*time.Second), true)
	parsedAt(t, s, "bsc", 900, m0.Add(2*time.Minute), false, 0)

	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: m0, USD: "100.5", Source: "coinbase"}}); err != nil {
		t.Fatal(err)
	}
	// A later quote for the same minute replaces the earlier one.
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: m0, USD: "101.25", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT price_usd::text || ' ' || source FROM quote_prices WHERE symbol = 'SOL' AND minute = $1`, m0); got != "101.25 binance" {
		t.Errorf("quote price = %s; want 101.25 binance", got)
	}

	got, err := s.MissingPriceMinutes(ctx, "solana", "SOL", m0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{m0.Add(time.Minute), m0.Add(3 * time.Minute)} // m0 priced; m0+3 has only a liquidity event
	if !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("solana missing minutes = %v; want %v", got, want)
	}
	if got, _ := s.MissingPriceMinutes(ctx, "bsc", "BNB", m0.Add(-time.Hour)); len(got) != 1 || !got[0].Equal(m0.Add(2*time.Minute)) {
		t.Errorf("bsc missing minutes = %v; want [%v]", got, m0.Add(2*time.Minute))
	}
	if got, _ := s.MissingPriceMinutes(ctx, "solana", "SOL", m0.Add(2*time.Minute)); len(got) != 1 || !got[0].Equal(m0.Add(3*time.Minute)) {
		t.Errorf("solana missing minutes since m0+2 = %v; want [m0+3]", got)
	}
}

func TestPruneComputeKeepsRetention(t *testing.T) {
	ctx := context.Background()
	s, c := openWithConn(t)
	now := day.Add(5*24*time.Hour + 12*time.Hour)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := c.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	for _, ago := range []time.Duration{2 * time.Hour, 25 * time.Hour, 31 * 24 * time.Hour} {
		at := now.Add(-ago).Truncate(time.Minute)
		exec(`INSERT INTO pool_liquidity VALUES ('solana', 'p', $1, 1)`, at)
		exec(`INSERT INTO quote_prices (symbol, minute, price_usd, source) VALUES ('SOL', $1, 1, 'binance')`, at)
		for _, iv := range []string{"1m", "1h"} {
			exec(`INSERT INTO candles (chain, token_address, interval, t, pool_id, o, h, l, c, closed) VALUES ('solana', 'm', $2, $1, 'p', 1, 1, 1, 1, true)`, at, iv)
		}
		exec(`INSERT INTO stats_dirty VALUES ('solana', $1)`, at)
	}
	for _, ago := range []time.Duration{time.Hour, 4 * 24 * time.Hour} {
		exec(`CREATE TABLE IF NOT EXISTS pool_minute_stats_` + now.Add(-ago).Format("20060102") + ` PARTITION OF pool_minute_stats FOR VALUES FROM ('` +
			now.Add(-ago).Truncate(24*time.Hour).Format(time.RFC3339) + `') TO ('` + now.Add(-ago).Truncate(24*time.Hour).Add(24*time.Hour).Format(time.RFC3339) + `')`)
	}
	if err := s.PruneCompute(ctx, now); err != nil {
		t.Fatal(err)
	}
	got := one(t, c, `SELECT concat_ws(' ', (SELECT count(*) FROM pool_liquidity), (SELECT count(*) FROM quote_prices),
		(SELECT count(*) FROM candles WHERE interval = '1m'), (SELECT count(*) FROM candles WHERE interval = '1h'), (SELECT count(*) FROM stats_dirty),
		(SELECT string_agg(c.relname, ',' ORDER BY c.relname) FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid WHERE i.inhparent = 'pool_minute_stats'::regclass))`)
	// pool_liquidity 1 day, quote_prices and 1m candles 30 days, other candles kept,
	// stats_dirty and pool_minute_stats partitions 3 days.
	if want := "1 2 2 3 2 pool_minute_stats_" + now.Add(-time.Hour).Format("20060102"); got != want {
		t.Errorf("after prune = %s; want %s", got, want)
	}
}
