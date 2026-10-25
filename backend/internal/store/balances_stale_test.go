package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestApplyBalancesRejectsRewrittenRaw(t *testing.T) {
	ctx := context.Background()
	s, c := openWithConn(t)
	b := block(501, store.StatusConfirmed)
	if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: b}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: b.Num, Time: b.Time, Version: 7}); err != nil {
		t.Fatal(err)
	}
	stale := store.BalanceBlock{Chain: "solana", Num: b.Num, Time: b.Time, Hash: b.Hash, Version: 1, Deltas: []parse.Balance{{Token: mint, Owner: user, Delta: "100"}}}
	b.Hash = []byte{99}
	if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: b}); err != nil {
		t.Fatal(err)
	}
	// Reorg can finish the new main parse while the old balance decoder is
	// still working. Neither an unparsed rewrite nor the new hash accepts it.
	for _, reparsed := range []bool{false, true} {
		if reparsed {
			if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: b.Num, Time: b.Time, Version: 7}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.ApplyBalances(ctx, []store.BalanceBlock{stale}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("stale write after main=%v: %v;want ErrNotFound", reparsed, err)
		}
		if got := one(t, c, "SELECT count(*)::text FROM balance_deltas"); got != "0" {
			t.Errorf("stale deltas persisted=%s", got)
		}
		if got := one(t, c, "SELECT balance_version::text FROM chain_blocks"); got != "NULL" {
			t.Errorf("stale progress=%s", got)
		}
	}
}
