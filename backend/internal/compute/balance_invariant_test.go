package compute_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"log/slog"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestBalanceInvariantChecksOnlyFullTokens(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	at := day.Add(time.Hour)
	token, owner := evm.Address{1}, evm.Address{2}
	dead := evm.MustAddress("0x000000000000000000000000000000000000dead")
	apply(t, s, "bsc", 1, at, parse.Facts{Tokens: []parse.Token{{Address: token[:], Created: true, TotalSupply: "100"}}})
	apply(t, s, "bsc", 2, at.Add(time.Second), parse.Facts{})
	write := func(amount string) {
		t.Helper()
		if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 1, Time: at, Version: 1, Deltas: []parse.Balance{{Token: token[:], Owner: owner[:], Delta: amount}, {Token: token[:], Owner: dead[:], Delta: "20"}}}}); err != nil {
			t.Fatal(err)
		}
	}
	write("79")
	cov := compute.NewCoverage(s, []string{"bsc"})
	if err := cov.Step(ctx); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(prior)
	before := testutil.ToFloat64(metrics.BalanceInvariantViolations)
	b := compute.NewBalances(s, []string{"bsc"})
	if err := b.Step(ctx, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(metrics.BalanceInvariantViolations) - before; got != 0 {
		t.Errorf("partial invariant violations=%v;want 0", got)
	}
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 2, Time: at.Add(time.Second), Version: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Step(ctx, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(metrics.BalanceInvariantViolations) - before; got != 1 {
		t.Errorf("full invariant violations=%v;want 1", got)
	}
	if !strings.Contains(logs.String(), "balance conservation mismatch") || !strings.Contains(logs.String(), `"chain":"bsc"`) {
		t.Errorf("missing conservation warning: %s", logs.String())
	}
	// 80 circulating + 20 burned equals the creation supply of 100.
	write("80")
	if err := b.Step(ctx, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(metrics.BalanceInvariantViolations) - before; got != 1 {
		t.Errorf("balanced token produced a warning: %v", got)
	}
}
