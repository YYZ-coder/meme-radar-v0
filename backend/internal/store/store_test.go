package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

func open(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, testdb.New(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

var day = time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)

func TestCommitForwardBlockIsReadable(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	payload := bytes.Repeat([]byte(`{"tx":"abc"}`), 1000)
	b := store.Block{Num: 100, Hash: []byte{1, 2}, ParentHash: []byte{0, 1}, Time: day.Add(time.Hour),
		Status: store.StatusConfirmed, TxCount: 3, Payload: payload, Source: "test"}
	if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: b,
		Forward: &store.Cursor{Anchor: 100, Position: 100, Head: 104}}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got, err := s.RawBlock(ctx, "solana", 100)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("RawBlock = %d bytes, %v; want the %d committed bytes", len(got), err, len(payload))
	}
	if h, err := s.BlockHash(ctx, "solana", 100); err != nil || !bytes.Equal(h, []byte{1, 2}) {
		t.Fatalf("BlockHash = %x, %v; want 0102", h, err)
	}
	c, err := s.ForwardCursor(ctx, "solana")
	if want := (store.Cursor{Anchor: 100, Position: 100, Head: 104}); err != nil || c != want {
		t.Fatalf("ForwardCursor = %+v, %v; want %+v", c, err, want)
	}
	if _, err := s.ForwardCursor(ctx, "base"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ForwardCursor(base) err = %v; want ErrNotFound", err)
	}
}

func TestMigrateTwiceKeepsSchema(t *testing.T) {
	ctx := context.Background()
	s := open(t) // migrated once
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if _, err := s.ForwardCursor(ctx, "solana"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ForwardCursor on empty schema err = %v; want ErrNotFound", err)
	}
}

func block(num uint64, status string) store.Block {
	b := store.Block{Num: num, Status: status, Source: "test"}
	if status != store.StatusSkipped {
		b.Hash, b.ParentHash = []byte{byte(num)}, []byte{byte(num - 1)}
		b.Time, b.Payload, b.TxCount = day.Add(time.Duration(num)*time.Second), []byte(`{}`), 1
	}
	return b
}

func TestGapsShrinkTopDownUntilFilled(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	// Forward jumps from 10 to 14: blocks 11..13 become a gap.
	if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: block(14, store.StatusConfirmed),
		Forward: &store.Cursor{Anchor: 10, Position: 14, Head: 14},
		NewGap:  &store.Gap{From: 11, To: 13, Reason: store.GapRateLimited}}); err != nil {
		t.Fatalf("Commit 14: %v", err)
	}
	gaps, err := s.OpenGaps(ctx, "solana")
	if err != nil || len(gaps) != 1 || gaps[0].From != 11 || gaps[0].To != 13 || gaps[0].Reason != store.GapRateLimited {
		t.Fatalf("OpenGaps = %+v, %v; want one rate_limited gap 11..13", gaps, err)
	}
	id := gaps[0].ID
	for _, b := range []store.Block{block(13, store.StatusConfirmed), block(12, store.StatusSkipped)} {
		if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: b, FillGap: id}); err != nil {
			t.Fatalf("fill %d: %v", b.Num, err)
		}
	}
	if gaps, err = s.OpenGaps(ctx, "solana"); err != nil || len(gaps) != 1 || gaps[0].From != 11 || gaps[0].To != 11 {
		t.Fatalf("after filling 13 and 12, OpenGaps = %+v, %v; want gap 11..11", gaps, err)
	}
	if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: block(11, store.StatusConfirmed), FillGap: id}); err != nil {
		t.Fatalf("fill 11: %v", err)
	}
	if gaps, err = s.OpenGaps(ctx, "solana"); err != nil || len(gaps) != 0 {
		t.Fatalf("after filling 11, OpenGaps = %+v, %v; want none", gaps, err)
	}
	if c, err := s.ForwardCursor(ctx, "solana"); err != nil || c.Position != 14 {
		t.Fatalf("gap fills moved the forward cursor: %+v, %v; want position 14", c, err)
	}
}

func TestPromoteAndRollback(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for n := uint64(1); n <= 5; n++ {
		if err := s.Commit(ctx, store.Commit{Chain: "base", Block: block(n, store.StatusPending),
			Forward: &store.Cursor{Anchor: 1, Position: n, Head: 5}}); err != nil {
			t.Fatalf("Commit %d: %v", n, err)
		}
	}
	if err := s.PromoteConfirmed(ctx, "base", 3); err != nil {
		t.Fatalf("PromoteConfirmed: %v", err)
	}
	for n, want := range map[uint64]string{1: store.StatusConfirmed, 3: store.StatusConfirmed, 4: store.StatusPending} {
		if got, err := s.BlockStatus(ctx, "base", n); err != nil || got != want {
			t.Errorf("BlockStatus(%d) = %q, %v; want %q", n, got, err, want)
		}
	}
	if err := s.Rollback(ctx, "base", 3); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	for _, n := range []uint64{4, 5} {
		if _, err := s.BlockHash(ctx, "base", n); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("BlockHash(%d) after rollback err = %v; want ErrNotFound", n, err)
		}
		if _, err := s.RawBlock(ctx, "base", n); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("RawBlock(%d) after rollback err = %v; want ErrNotFound", n, err)
		}
	}
	if _, err := s.RawBlock(ctx, "base", 3); err != nil {
		t.Errorf("RawBlock(3) after rollback: %v; want kept", err)
	}
	if c, err := s.ForwardCursor(ctx, "base"); err != nil || c.Position != 3 {
		t.Errorf("ForwardCursor after rollback = %+v, %v; want position 3", c, err)
	}
}

func TestLockChainIsExclusivePerChain(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	a, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("Open a: %v", err)
	}
	defer a.Close()
	b, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("Open b: %v", err)
	}
	defer b.Close()
	unlock, err := a.LockChain(ctx, "bsc")
	if err != nil {
		t.Fatalf("a.LockChain(bsc): %v", err)
	}
	if _, err := b.LockChain(ctx, "bsc"); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("b.LockChain(bsc) while held err = %v; want ErrLocked", err)
	}
	other, err := b.LockChain(ctx, "base")
	if err != nil {
		t.Fatalf("b.LockChain(base): %v; want independent of bsc", err)
	}
	other()
	unlock()
	again, err := b.LockChain(ctx, "bsc")
	if err != nil {
		t.Fatalf("b.LockChain(bsc) after unlock: %v", err)
	}
	again()
}

func TestPruneDropsExpiredRawPartitions(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	old, recent := block(1, store.StatusConfirmed), block(2, store.StatusConfirmed)
	old.Time = day.AddDate(0, 0, -10)
	for _, b := range []store.Block{old, recent} {
		if err := s.Commit(ctx, store.Commit{Chain: "bsc", Block: b}); err != nil {
			t.Fatalf("Commit %d: %v", b.Num, err)
		}
	}
	if err := s.Prune(ctx, day.AddDate(0, 0, -7), day.AddDate(0, 0, -30)); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := s.RawBlock(ctx, "bsc", 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("RawBlock(10 days old) err = %v; want ErrNotFound", err)
	}
	if _, err := s.RawBlock(ctx, "bsc", 2); err != nil {
		t.Errorf("RawBlock(recent): %v; want kept", err)
	}
	if _, err := s.BlockHash(ctx, "bsc", 1); err != nil {
		t.Errorf("ledger row of 10-day-old block: %v; want kept for 30 days", err)
	}
	// The dropped partition is recreated when a block of that day arrives again.
	if err := s.Commit(ctx, store.Commit{Chain: "bsc", Block: old}); err != nil {
		t.Fatalf("re-Commit old block after prune: %v", err)
	}
	if err := s.Prune(ctx, day.AddDate(0, 0, -7), day.AddDate(0, 0, -5)); err != nil {
		t.Fatalf("Prune ledger: %v", err)
	}
	if _, err := s.BlockHash(ctx, "bsc", 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ledger row older than ledgerBefore err = %v; want ErrNotFound", err)
	}
}

func TestForwardErrorIsClearedByNextCommit(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	commit := func(n uint64) {
		t.Helper()
		if err := s.Commit(ctx, store.Commit{Chain: "base", Block: block(n, store.StatusPending),
			Forward: &store.Cursor{Anchor: 1, Position: n, Head: n}}); err != nil {
			t.Fatalf("Commit %d: %v", n, err)
		}
	}
	commit(1)
	if err := s.SetForwardError(ctx, "base", "rate_limited", "429 retry after 10s"); err != nil {
		t.Fatalf("SetForwardError: %v", err)
	}
	if kind, msg, err := s.ForwardError(ctx, "base"); err != nil || kind != "rate_limited" || msg != "429 retry after 10s" {
		t.Fatalf("ForwardError = %q, %q, %v; want the recorded error", kind, msg, err)
	}
	commit(2)
	if kind, msg, err := s.ForwardError(ctx, "base"); err != nil || kind != "" || msg != "" {
		t.Fatalf("ForwardError after commit = %q, %q, %v; want cleared", kind, msg, err)
	}
}

func TestEnsureChainAddsConfiguredChain(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if err := s.Commit(ctx, store.Commit{Chain: "arbitrum", Block: block(1, store.StatusConfirmed)}); err == nil {
		t.Fatal("Commit on an unknown chain succeeded before EnsureChain; want an enum error")
	}
	for i := 0; i < 2; i++ { // idempotent
		if err := s.EnsureChain(ctx, "arbitrum"); err != nil {
			t.Fatalf("EnsureChain #%d: %v", i, err)
		}
	}
	if err := s.Commit(ctx, store.Commit{Chain: "arbitrum", Block: block(1, store.StatusConfirmed)}); err != nil {
		t.Fatalf("Commit after EnsureChain: %v", err)
	}
	if err := s.EnsureChain(ctx, "bad'name"); err == nil {
		t.Fatal("EnsureChain accepted an invalid name")
	}
}

func TestCommitBlocksIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if err := s.CommitBlocks(ctx, []store.Commit{
		{Chain: "base", Block: block(1, store.StatusPending), Forward: &store.Cursor{Anchor: 1, Position: 1, Head: 3}},
		{Chain: "base", Block: block(2, store.StatusPending), Forward: &store.Cursor{Anchor: 1, Position: 2, Head: 3}},
	}); err != nil {
		t.Fatalf("CommitBlocks: %v", err)
	}
	if c, _ := s.ForwardCursor(ctx, "base"); c.Position != 2 {
		t.Fatalf("cursor = %+v; want position 2", c)
	}
	// The second commit fills a gap that does not exist: nothing is written.
	err := s.CommitBlocks(ctx, []store.Commit{
		{Chain: "base", Block: block(3, store.StatusPending), Forward: &store.Cursor{Anchor: 1, Position: 3, Head: 3}},
		{Chain: "base", Block: block(9, store.StatusConfirmed), FillGap: 12345},
	})
	if err == nil {
		t.Fatal("CommitBlocks with an invalid gap fill succeeded")
	}
	if _, err := s.BlockHash(ctx, "base", 3); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("block 3 after the failed batch: %v; want not written", err)
	}
	if c, _ := s.ForwardCursor(ctx, "base"); c.Position != 2 {
		t.Errorf("cursor after the failed batch = %+v; want still 2", c)
	}
}

func TestRawRetentionZeroKeepsRawUntilPrunedByHand(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for i, chain := range []string{"bsc", "base"} {
		b := block(uint64(1+i), store.StatusConfirmed)
		b.Time = day.AddDate(0, 0, -40)
		if err := s.Commit(ctx, store.Commit{Chain: chain, Block: b}); err != nil {
			t.Fatal(err)
		}
		r := block(uint64(10+i), store.StatusConfirmed)
		r.Time = day.AddDate(0, 0, -2)
		if err := s.Commit(ctx, store.Commit{Chain: chain, Block: r}); err != nil {
			t.Fatal(err)
		}
	}
	// No raw cutoff: only the ledger and facts expire automatically.
	if err := s.Prune(ctx, time.Time{}, day.AddDate(0, 0, -60)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RawBlock(ctx, "bsc", 1); err != nil {
		t.Fatalf("raw block after Prune without a raw cutoff: %v; want kept", err)
	}
	// By hand for one chain: only that chain's older rows go.
	if err := s.PruneRaw(ctx, day.AddDate(0, 0, -30), "bsc"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RawBlock(ctx, "bsc", 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("bsc old raw after PruneRaw(bsc): %v; want ErrNotFound", err)
	}
	if _, err := s.RawBlock(ctx, "base", 2); err != nil {
		t.Errorf("base old raw after PruneRaw(bsc): %v; want kept", err)
	}
	// For all chains: whole days before the cutoff are dropped.
	if err := s.PruneRaw(ctx, day.AddDate(0, 0, -30), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RawBlock(ctx, "base", 2); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("base old raw after PruneRaw(all): %v; want ErrNotFound", err)
	}
	for _, c := range []struct {
		chain string
		n     uint64
	}{{"bsc", 10}, {"base", 11}} {
		if _, err := s.RawBlock(ctx, c.chain, c.n); err != nil {
			t.Errorf("recent %s raw: %v; want kept", c.chain, err)
		}
	}
}
