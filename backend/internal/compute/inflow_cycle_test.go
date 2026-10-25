package compute_test

import (
	"math"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// A newly parsed member trade must be copied with this pass's filled price,
// even during an outage of external price providers. No second pass is needed.
func TestCycleCopiesSmTradesAfterValues(t *testing.T) {
	s, c := open(t)
	now := day.Add(12 * time.Hour)
	version := h4Members(t, s, now, false)
	at := now.Add(-time.Minute)
	h4Quotes(t, s, at)
	apply(t, s, "bsc", 1, at, h4Facts(h4Trade(1, 1, "5000000001")))
	cycle := compute.NewCycle(compute.CycleConfig{Store: s, Prices: downSource{}, Chains: []compute.ChainNative{{Chain: "bsc", Symbol: "BNB"}}, Lookback: time.Hour, FullEvery: time.Minute})
	if err := cycle.Run(t.Context(), now); err == nil {
		t.Fatal("price outage should still be reported")
	}
	if got := one(t, c, `SELECT concat_ws(' ',count(*),max(trim_scale(value_usd)::text),bool_and(is_large)::text) FROM sm_trades WHERE version=$1`, version); got != "1 5000.000001 true" {
		t.Fatalf("same-pass priced copy=%s; want 1 5000.000001 true", got)
	}
}

// H4-12d: the backlog gauge is measured from the persisted head's cutoff,
// even while a projection has stopped publishing. The hand-calculated age is 90s.
func TestCycleReportsProjectionAgeFromPersistedHead(t *testing.T) {
	s, _ := open(t)
	now := day.Add(12 * time.Hour)
	apply(t, s, "bsc", 1, now.Add(-6*time.Minute), h4Facts())
	apply(t, s, "bsc", 2, now.Add(-time.Minute), h4Facts(h4Trade(1, 1, "1000000")))
	apply(t, s, "bsc", 3, now.Add(time.Second), parse.Facts{})
	for _, err := range []error{s.RefreshStats(t.Context(), "bsc", now), s.RefreshSmTrades(t.Context(), "bsc", now), s.RefreshCoverage(t.Context(), "bsc")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RebuildInflow(t.Context(), "bsc", "5m", now); err != nil {
		t.Fatal(err)
	}
	cycle := compute.NewCycle(compute.CycleConfig{Store: s, Prices: downSource{}, Chains: []compute.ChainNative{{Chain: "bsc", Symbol: "BNB"}}, Lookback: time.Hour, FullEvery: time.Minute})
	_ = cycle.Run(t.Context(), now.Add(90*time.Second))
	if got := testutil.ToFloat64(metrics.ProjectionAgeSeconds.WithLabelValues("bsc", "5m")); got != 90 {
		t.Errorf("projection age=%v; want 90 seconds from persisted cutoff", got)
	}
}

// H4-12h observes the public backlog metric boundary. The approved value for
// every configured chain/window without a first head is positive infinity.
func TestCycleReportsPositiveInfinityBeforeFirstProjection(t *testing.T) {
	s, _ := open(t)
	now := day.Add(12 * time.Hour)
	cycle := compute.NewCycle(compute.CycleConfig{Store: s, Prices: downSource{}, Chains: []compute.ChainNative{{Chain: "bsc", Symbol: "BNB"}, {Chain: "base", Symbol: "ETH"}, {Chain: "solana", Symbol: "SOL"}}, Lookback: time.Hour, FullEvery: time.Minute})
	if err := cycle.Run(t.Context(), now); err == nil {
		t.Fatal("price outage should still be reported")
	}
	heads, err := s.InflowHeads(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(heads) != 0 {
		t.Fatalf("fixture unexpectedly published %d heads", len(heads))
	}
	for _, chain := range []string{"bsc", "base", "solana"} {
		for _, window := range []string{"1m", "5m", "1h", "4h", "24h"} {
			got := testutil.ToFloat64(metrics.ProjectionAgeSeconds.WithLabelValues(chain, window))
			if !math.IsInf(got, 1) {
				t.Errorf("first head absent: %s/%s age=%v; want +Inf", chain, window, got)
			}
		}
	}
}
