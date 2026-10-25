package parser_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/parser"
	"github.com/meme-radar/radar/internal/store"
)

func TestBalancePassYieldsAboveMainBacklogThreshold(t *testing.T) {
	ctx := context.Background()
	s, c := setup(t)
	at := time.Unix(1792719992, 0)
	var cs []store.Commit
	for n := uint64(1); n <= 1002; n++ {
		cs = append(cs, store.Commit{Chain: "solana", Block: store.Block{Num: n, Hash: []byte{byte(n), byte(n >> 8)}, Time: at, Status: store.StatusConfirmed, Payload: []byte(`{"blockTime":1792719992,"transactions":[]}`), Source: "test"}})
	}
	if err := s.CommitBlocks(ctx, cs); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: 1, Time: at, Version: 1}); err != nil {
		t.Fatal(err)
	}
	pass := parser.NewBalances(parser.BalanceConfig{Chain: "solana", Family: "solana", Store: s, Batch: 1})
	if _, err := pass.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if got := query(t, c, "SELECT count(*)::text FROM chain_blocks WHERE balance_version IS NOT NULL"); got != "0" {
		t.Errorf("1001 pending main blocks did not pause balances: %s", got)
	}
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: 1002, Time: at, Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := pass.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if got := query(t, c, "SELECT coalesce(balance_version,0)::text FROM chain_blocks WHERE block_num=1002"); got != "1" {
		t.Errorf("1000 pending main blocks did not resume newest balance block: %s", got)
	}
}
