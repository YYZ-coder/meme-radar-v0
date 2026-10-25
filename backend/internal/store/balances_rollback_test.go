package store_test

import (
	"context"
	"testing"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestRewriteAndRollbackClearBalances(t *testing.T) {
	ctx := context.Background()
	s, c := openWithConn(t)
	b := block(501, store.StatusConfirmed)
	commit := func() {
		t.Helper()
		if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: b}); err != nil {
			t.Fatal(err)
		}
	}
	main := func() {
		t.Helper()
		if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: b.Num, Time: b.Time, Version: 7}); err != nil {
			t.Fatal(err)
		}
	}
	commit()
	main()
	p := store.BalanceBlock{Chain: "solana", Num: b.Num, Time: b.Time, Version: 1, Deltas: []parse.Balance{{Tx: 1, Token: mint, Owner: user, Delta: "100"}}}
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{p}); err != nil {
		t.Fatal(err)
	}
	b.Hash = []byte{9, 9}
	commit()
	for _, col := range []string{"parsed_version", "parsed_at", "balance_version", "balance_parsed_at"} {
		if got := one(t, c, "SELECT "+col+"::text FROM chain_blocks"); got != "NULL" {
			t.Errorf("rewrite retained %s=%s;want NULL", col, got)
		}
	}
	if blocks, err := s.UnbalancedBlocks(ctx, "solana", 1, 10); err != nil || len(blocks) != 0 {
		t.Fatalf("rewritten unparsed blocks=%v,%v;want none", blocks, err)
	}
	main()
	if blocks, err := s.UnbalancedBlocks(ctx, "solana", 1, 10); err != nil || len(blocks) != 1 {
		t.Fatalf("reparsed block eligible=%v,%v;want one", blocks, err)
	}
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{p}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rollback(ctx, "solana", 500); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, "SELECT count(*)::text FROM balance_deltas"); got != "0" {
		t.Errorf("rollback deltas=%s;want 0", got)
	}
}
