package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestCycleAccumulatesBalancesAndReportsRows(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	at := day.Add(time.Hour)
	apply(t, s, "bsc", 1, at, parse.Facts{Tokens: []parse.Token{{Address: []byte("T")}}})
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 1, Time: at, Version: 1, Deltas: []parse.Balance{{Token: []byte("T"), Owner: []byte("A"), Delta: "100"}}}}); err != nil {
		t.Fatal(err)
	}
	cycle := compute.NewCycle(compute.CycleConfig{Store: s, Chains: []compute.ChainNative{{Chain: "bsc", Symbol: "BNB"}}, Prices: &fakeSource{latest: map[string]string{"BNB": "100"}}, Lookback: 24 * time.Hour, FullEvery: time.Minute})
	if err := cycle.Run(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, "SELECT coalesce(sum(balance),0)::text FROM token_balances"); got != "100" {
		t.Errorf("cycle balance=%s;want 100", got)
	}
	if got := testutil.ToFloat64(metrics.TokenBalancesRows.WithLabelValues("bsc")); got != 1 {
		t.Errorf("balance row gauge=%v;want 1", got)
	}
	// A normal incremental pass also consumes the next balance change.
	apply(t, s, "bsc", 2, at.Add(2*time.Second), parse.Facts{})
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 2, Time: at.Add(2 * time.Second), Version: 1, Deltas: []parse.Balance{{Token: []byte("T"), Owner: []byte("A"), Delta: "-5"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := cycle.Run(ctx, at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, "SELECT coalesce(sum(balance),0)::text FROM token_balances"); got != "95" {
		t.Errorf("incremental cycle balance=%s;want 95", got)
	}
}
