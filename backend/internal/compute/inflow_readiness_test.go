package compute_test

import (
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

// H4-9b follows the user's publication rule at the agreed head boundary.
// Existing compute_marks and dirty queues must cover the required facts.
func TestProjectionWaitsForStatsAndSmTrades(t *testing.T) {
	s, c := open(t)
	now := day.Add(19 * time.Hour)
	apply(t, s, "bsc", 1, now.Add(-10*time.Minute), parse.Facts{})
	apply(t, s, "bsc", 2, now.Add(-time.Minute), h4Facts(h4Trade(1, 1, "1000000")))
	apply(t, s, "bsc", 3, now.Add(time.Second), parse.Facts{})
	if err := s.RefreshCoverage(t.Context(), "bsc"); err != nil {
		t.Fatal(err)
	}
	initial, err := s.RebuildInflow(t.Context(), "bsc", "5m", now)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Rebuilt || initial.Version != 0 {
		t.Fatalf("initial stats lag published a head: %+v; want no head", initial)
	}
	if got := one(t, c, `SELECT count(*)::text FROM projection_heads`); got != "0" {
		t.Fatalf("head before readiness=%s, want 0", got)
	}
	if err = s.RefreshStats(t.Context(), "bsc", now); err != nil {
		t.Fatal(err)
	}
	initial, err = s.RebuildInflow(t.Context(), "bsc", "5m", now)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Rebuilt || initial.Version != 0 {
		t.Fatalf("initial smtrades lag published a head: %+v", initial)
	}
	if err = s.RefreshSmTrades(t.Context(), "bsc", now); err != nil {
		t.Fatal(err)
	}
	old, err := s.RebuildInflow(t.Context(), "bsc", "5m", now)
	if err != nil || !old.Rebuilt || old.Rows != 1 {
		t.Fatalf("first ready publication=%+v err=%v, want 1 row", old, err)
	}
	makeFacts := func(tails ...byte) parse.Facts {
		f := parse.Facts{}
		for i, tail := range tails {
			six := int16(6)
			tr := h4Trade(i+1, 1, "1000000")
			tr.Token = h4Key(tail)
			tr.Pool = h4Key(tail + 10)
			f.Tokens = append(f.Tokens, parse.Token{Address: tr.Token, Decimals: &six, Created: true})
			f.Pools = append(f.Pools, parse.Pool{ID: tr.Pool, Token: tr.Token, Quote: h4Key(99), Dex: "test", Protocol: "test"})
			f.Trades = append(f.Trades, tr)
		}
		return f
	}
	apply(t, s, "bsc", 4, now.Add(30*time.Second), makeFacts(81))
	apply(t, s, "bsc", 5, now.Add(time.Minute+time.Second), parse.Facts{})
	if err = s.RefreshCoverage(t.Context(), "bsc"); err != nil {
		t.Fatal(err)
	}
	if err = s.RefreshSmTrades(t.Context(), "bsc", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	checkOld := func(label string) {
		t.Helper()
		got, err := s.RebuildInflow(t.Context(), "bsc", "5m", now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if got.Rebuilt || got.Version != old.Version {
			t.Fatalf("%s published while behind: %+v, old=%d", label, got, old.Version)
		}
		if head := one(t, c, `SELECT projection_version::text FROM projection_heads`); head != one(t, c, `SELECT $1::bigint::text`, old.Version) {
			t.Fatalf("%s changed head to %s", label, head)
		}
	}
	checkOld("stats lag")
	if err = s.ApplyParsed(t.Context(), store.Parsed{Chain: "bsc", Num: 4, Time: now.Add(30 * time.Second), Version: 2, Facts: makeFacts(81, 82)}); err != nil {
		t.Fatal(err)
	}
	if err = s.RefreshStats(t.Context(), "bsc", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	checkOld("smtrades lag")
	if err = s.RefreshSmTrades(t.Context(), "bsc", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	ready, err := s.RebuildInflow(t.Context(), "bsc", "5m", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !ready.Rebuilt || ready.Version == old.Version || ready.Rows != 3 {
		t.Fatalf("caught-up version=%+v, want new complete 3-row version", ready)
	}
}
