package store_test

import (
	"context"
	"testing"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestApplyBalancesWritesDeltasAndVersion(t *testing.T) {
	ctx := context.Background()
	s, c := openWithConn(t)
	b := block(501, store.StatusConfirmed)
	if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: b}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: b.Num, Time: b.Time, Version: 7, Facts: facts("700", "30", "active")}); err != nil {
		t.Fatal(err)
	}
	p := store.BalanceBlock{Chain: "solana", Num: b.Num, Time: b.Time, Version: 1, Deltas: []parse.Balance{{Tx: 1, Token: mint, Owner: user, Delta: "100"}, {Tx: 2, Token: mint, Owner: curve, Delta: "-30"}}}
	for range 2 {
		if err := s.ApplyBalances(ctx, []store.BalanceBlock{p}); err != nil {
			t.Fatal(err)
		}
	}
	for sql, want := range map[string]string{
		"SELECT count(*)::text FROM balance_deltas":                      "2",
		"SELECT balance_version::text FROM chain_blocks":                 "1",
		"SELECT (balance_parsed_at IS NOT NULL)::text FROM chain_blocks": "true",
		"SELECT parsed_version::text FROM chain_blocks":                  "7",
		"SELECT count(*)::text FROM trades":                              "1",
	} {
		if got := one(t, c, sql); got != want {
			t.Errorf("%s = %s;want %s", sql, got, want)
		}
	}
	p.Deltas = []parse.Balance{{Tx: 1, Token: mint, Owner: curve, Delta: "9"}}
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{p}); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, "SELECT count(*)::text || '|' || sum(delta)::text FROM balance_deltas"); got != "1|9" {
		t.Errorf("replacement=%s;want 1|9", got)
	}
	stamp := one(t, c, "SELECT balance_parsed_at::text FROM chain_blocks")
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: b.Num, Time: b.Time, Version: 8}); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, "SELECT balance_parsed_at::text FROM chain_blocks"); got != stamp {
		t.Errorf("main reparse changed balance timestamp: %s -> %s", stamp, got)
	}
	if got := one(t, c, "SELECT balance_version::text FROM chain_blocks"); got != "1" {
		t.Errorf("main reparse changed balance version: %s", got)
	}
	if got := one(t, c, "SELECT count(*)::text || '|' || sum(delta)::text FROM balance_deltas"); got != "1|9" {
		t.Errorf("main reparse changed balances: %s", got)
	}
}
