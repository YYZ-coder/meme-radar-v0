package parser_test

import (
	"context"
	"github.com/meme-radar/radar/internal/parser"
	"github.com/meme-radar/radar/internal/store"
	"testing"
	"time"
)

func TestBalanceDecodeFailureDoesNotBlockOtherBlocks(t *testing.T) {
	ctx := context.Background()
	s, c := setup(t)
	at := time.Unix(1792719992, 0)
	commit(t, s, 1, []byte(`{"blockTime":1792719992,"transactions":[]}`))
	commit(t, s, 2, []byte(`{`))
	for _, n := range []uint64{1, 2} {
		if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: n, Time: at, Version: 1}); err != nil {
			t.Fatal(err)
		}
	}
	pass := parser.NewBalances(parser.BalanceConfig{Chain: "solana", Family: "solana", Store: s, Batch: 2})
	if _, err := pass.Step(ctx); err == nil {
		t.Error("malformed block must report a decode error")
	}
	if got := query(t, c, "SELECT coalesce(balance_version,0)::text FROM chain_blocks WHERE block_num=1"); got != "1" {
		t.Errorf("valid block did not make progress: %s", got)
	}
	if got := query(t, c, "SELECT coalesce(balance_version,0)::text FROM chain_blocks WHERE block_num=2"); got != "0" {
		t.Errorf("malformed block was marked complete: %s", got)
	}
}
