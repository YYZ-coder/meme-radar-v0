package parser_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/parser"
	"github.com/meme-radar/radar/internal/store"
)

func TestBalanceRunResumesAndReportsProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, c := setup(t)
	commit(t, s, 10, []byte(`{"blockTime":1792719992,"transactions":[]}`))
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: 10, Time: time.Unix(1792719992, 0), Version: 7}); err != nil {
		t.Fatal(err)
	}
	before := testutil.ToFloat64(metrics.BalanceBlocksApplied.WithLabelValues("solana"))
	p := parser.NewBalances(parser.BalanceConfig{Chain: "solana", Family: "solana", Store: s, Batch: 1, PollInterval: time.Millisecond})
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for query(t, c, "SELECT coalesce(max(balance_version),0)::text FROM chain_blocks") != "1" {
		select {
		case err := <-done:
			t.Fatalf("balance Run returned before progress: %v", err)
		case <-timer.C:
			t.Fatal("balance Run did not write progress")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run failed to stop on cancellation")
	}
	if got := testutil.ToFloat64(metrics.BalanceBlocksApplied.WithLabelValues("solana")) - before; got != 1 {
		t.Errorf("applied blocks=%v;want 1", got)
	}
}
