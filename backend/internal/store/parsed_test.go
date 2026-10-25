package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

// openWithConn returns a migrated store and a plain connection for checking
// table contents (the persistence contract of 02-data-model §7.1, §3, §4).
func openWithConn(t *testing.T) (*store.Store, *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	url := testdb.New(t)
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return s, c
}

func one(t *testing.T, c *pgx.Conn, sql string, args ...any) string {
	t.Helper()
	var v *string
	if err := c.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if v == nil {
		return "NULL"
	}
	return *v
}

var (
	mint  = []byte("mint-0000000000000000000000000000")
	curve = []byte("curve-000000000000000000000000000")
	wsol  = []byte("wsol-0000000000000000000000000000")
	amm   = []byte("amm-00000000000000000000000000000")
	user  = []byte("user-0000000000000000000000000000")
)

func dec(v int16) *int16 { return &v }

func facts(rt, rq string, status string) parse.Facts {
	return parse.Facts{
		Tokens: []parse.Token{{Address: mint, Created: true, Name: "Doge\x00coin\u0007", Symbol: "DOGE", URI: "https://x/m",
			Decimals: dec(6), TotalSupply: "1000000000000000", Creator: user, LaunchPlatform: "pump_fun"}},
		Pools: []parse.Pool{{ID: curve, Token: mint, Quote: wsol, QuoteSymbol: "SOL", Protocol: "pump_curve", Dex: "pump_fun",
			Created: true, Status: status, ReserveToken: rt, ReserveQuote: rq, Tx: 3, Ix: 7}},
		Trades: []parse.Trade{{Tx: 3, Ix: 7, TxHash: []byte{1}, Pool: curve, Token: mint, Wallet: user, Attribution: "confirmed",
			Side: "buy", TokenAmount: "1000", QuoteAmount: "2000000000", QuoteSymbol: "SOL", PriceNative: "0.000000002000000000",
			ValueNative: "2.000000000", Decoder: "pump_fun", Version: 1}},
		Liquidity: []parse.Liquidity{{Tx: 4, Ix: 2, TxHash: []byte{2}, Pool: amm, Token: mint, Wallet: user, Kind: "add",
			TokenAmount: "5", QuoteAmount: "6", QuoteSymbol: "SOL", ValueNative: "0.000000006", Decoder: "pumpswap", Version: 1}},
		Failures: []parse.Failure{{Tx: 9, TxHash: []byte{9}, Decoder: "pumpswap", Err: "bad event"}},
	}
}

func TestApplyParsedWritesFactsIdempotently(t *testing.T) {
	ctx := context.Background()
	s, c := openWithConn(t)
	at := day.Add(5 * time.Hour)
	b := block(500, store.StatusConfirmed)
	b.Time = at
	if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: b}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	p := store.Parsed{Chain: "solana", Num: 500, Time: at, Version: 1, Facts: facts("700", "30", "active"), HitTxs: 3, Events: 4}
	for i := 0; i < 2; i++ { // the second run must replace, not duplicate
		if err := s.ApplyParsed(ctx, p); err != nil {
			t.Fatalf("ApplyParsed #%d: %v", i, err)
		}
	}
	for sql, want := range map[string]string{
		"SELECT count(*)::text FROM trades WHERE chain = 'solana' AND block_num = 500": "1",
		"SELECT count(*)::text FROM liquidity_events":                                  "1",
		"SELECT count(*)::text FROM decode_failures":                                   "1",
		"SELECT side || ' ' || token_amount || ' ' || quote_amount || ' ' || price_native || ' ' || value_native || ' ' || block_time::text FROM trades":                 "buy 1000 2000000000 0.000000002000000000 2.000000000 2026-10-23 05:00:00+00",
		"SELECT name || '|' || symbol || '|' || decimals || '|' || total_supply || '|' || launch_platform || '|' || launch_at::text || '|' || created_block FROM tokens": "Dogecoin|DOGE|6|1000000000000000|pump_fun|2026-10-23 05:00:00+00|500",
		"SELECT status || ' ' || reserve_token || ' ' || reserve_quote || ' ' || created_block FROM pools":                                                               "active 700 30 500",
		"SELECT parsed_version || ' ' || hit_txs || ' ' || events FROM chain_blocks WHERE block_num = 500":                                                               "1 3 4",
	} {
		if got := one(t, c, sql); got != want {
			t.Errorf("%s\n got  %s\n want %s", sql, got, want)
		}
	}
	if got, err := s.UnparsedBlocks(ctx, "solana", 10); err != nil || len(got) != 0 {
		t.Fatalf("UnparsedBlocks after parsing = %v, %v; want none", got, err)
	}
}

func TestApplyParsedKeepsNewestPoolStateAndGraduation(t *testing.T) {
	ctx := context.Background()
	s, c := openWithConn(t)
	newer, older := block(600, store.StatusConfirmed), block(590, store.StatusConfirmed)
	for _, b := range []store.Block{newer, older} {
		if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: b}); err != nil {
			t.Fatal(err)
		}
	}
	// Parsed newest first: block 600 graduates the curve, then the older
	// block 590 arrives with stale reserves and an "active" create.
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: 600, Time: newer.Time, Version: 1, Facts: facts("0", "85", "graduated")}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: 590, Time: older.Time, Version: 1, Facts: facts("900", "1", "active")}); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, "SELECT status || ' ' || reserve_token || ' ' || reserve_quote || ' ' || state_block FROM pools"); got != "graduated 0 85 600" {
		t.Errorf("pool = %s; want graduated with block 600's reserves (graduated 0 85 600)", got)
	}
	if got := one(t, c, "SELECT first_seen_at::text FROM tokens"); got != older.Time.UTC().Format("2006-01-02 15:04:05+00") {
		t.Errorf("token first_seen_at = %s; want the older block's time %s", got, older.Time.UTC())
	}
}

func TestUnparsedBlocksAndReset(t *testing.T) {
	ctx := context.Background()
	s, _ := openWithConn(t)
	for _, b := range []store.Block{block(10, store.StatusConfirmed), block(11, store.StatusSkipped), block(12, store.StatusConfirmed)} {
		if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: b}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.UnparsedBlocks(ctx, "solana", 10)
	if err != nil || len(got) != 2 || got[0].Num != 12 || got[1].Num != 10 || !got[0].Time.Equal(block(12, "").Time) {
		t.Fatalf("UnparsedBlocks = %+v, %v; want 12 then 10 (skipped 11 excluded) with block times", got, err)
	}
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: 12, Time: got[0].Time, Version: 1}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ResetParsed(ctx, "solana", 2); err != nil || n != 1 {
		t.Fatalf("ResetParsed(v2) = %d, %v; want 1 block reset", n, err)
	}
	if got, _ := s.UnparsedBlocks(ctx, "solana", 10); len(got) != 2 {
		t.Fatalf("after reset UnparsedBlocks = %+v; want both blocks again", got)
	}
	// A block rewritten by ingest (e.g. after a reorg) must be parsed again.
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: 10, Time: got[1].Time, Version: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: block(10, store.StatusConfirmed)}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.UnparsedBlocks(ctx, "solana", 10); len(got) != 2 || got[1].Num != 10 {
		t.Fatalf("after rewriting block 10, UnparsedBlocks = %+v; want it listed again", got)
	}
}

func TestFactsFollowConfirmationAndRollback(t *testing.T) {
	ctx := context.Background()
	s, c := openWithConn(t)
	for n := uint64(1); n <= 3; n++ {
		b := block(n, store.StatusPending)
		if err := s.Commit(ctx, store.Commit{Chain: "base", Block: b, Forward: &store.Cursor{Anchor: 1, Position: n, Head: 3}}); err != nil {
			t.Fatal(err)
		}
		f := facts("1", "1", "active")
		f.Trades[0].Tx = int(n) // distinct keys
		if err := s.ApplyParsed(ctx, store.Parsed{Chain: "base", Num: n, Time: b.Time, Version: 1, Facts: f}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PromoteConfirmed(ctx, "base", 2); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, "SELECT string_agg(block_num || ':' || confirmed, ',' ORDER BY block_num) FROM trades"); got != "1:true,2:true,3:false" {
		t.Errorf("trades confirmed = %s; want 1:true,2:true,3:false", got)
	}
	if got := one(t, c, "SELECT string_agg(block_num || ':' || confirmed, ',' ORDER BY block_num) FROM liquidity_events"); got != "1:true,2:true,3:false" {
		t.Errorf("liquidity confirmed = %s; want 1:true,2:true,3:false", got)
	}
	if err := s.Rollback(ctx, "base", 1); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"trades", "liquidity_events", "decode_failures"} {
		if got := one(t, c, "SELECT coalesce(string_agg(block_num::text, ','), '') FROM "+table); got != "1" {
			t.Errorf("%s blocks after rollback to 1 = %q; want only 1", table, got)
		}
	}
}

func TestApplyParsedManyIsAtomic(t *testing.T) {
	ctx := context.Background()
	s, c := openWithConn(t)
	for _, n := range []uint64{1, 2} {
		if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: block(n, store.StatusConfirmed)}); err != nil {
			t.Fatal(err)
		}
	}
	good := facts("1", "1", "active")
	bad := facts("1", "1", "active")
	bad.Trades[0].Attribution = "maybe" // violates the attribution CHECK
	err := s.ApplyParsedMany(ctx, []store.Parsed{
		{Chain: "solana", Num: 1, Time: block(1, "").Time, Version: 1, Facts: good},
		{Chain: "solana", Num: 2, Time: block(2, "").Time, Version: 1, Facts: bad},
	})
	if err == nil {
		t.Fatal("ApplyParsedMany with an invalid trade succeeded")
	}
	if got := one(t, c, "SELECT count(*)::text || ' ' || (SELECT count(*) FROM chain_blocks WHERE parsed_version IS NOT NULL) FROM trades"); got != "0 0" {
		t.Fatalf("after the failed batch trades/parsed = %s; want 0 0", got)
	}
	if err := s.ApplyParsedMany(ctx, []store.Parsed{
		{Chain: "solana", Num: 1, Time: block(1, "").Time, Version: 1, Facts: good},
		{Chain: "solana", Num: 2, Time: block(2, "").Time, Version: 1, Facts: facts("2", "2", "active")},
	}); err != nil {
		t.Fatalf("ApplyParsedMany: %v", err)
	}
	if got := one(t, c, "SELECT count(*)::text || ' ' || (SELECT count(*) FROM chain_blocks WHERE parsed_version = 1) FROM trades"); got != "2 2" {
		t.Fatalf("trades/parsed = %s; want 2 2", got)
	}
}
