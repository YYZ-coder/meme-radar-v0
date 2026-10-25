package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestCoverageRangesJoinParsedAndSkippedBlocks(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	t0 := day.Add(9 * time.Hour)
	at := func(n uint64) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	for _, n := range []uint64{1, 2, 3, 5, 7} {
		apply(t, s, "solana", n, at(n), parse.Facts{})
	}
	// 4 is a skipped slot; 6 is stored but not parsed yet.
	if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: store.Block{Num: 4, Status: store.StatusSkipped, Source: "test"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: store.Block{Num: 6, Hash: []byte{6}, ParentHash: []byte{5}, Time: at(6),
		Status: store.StatusConfirmed, TxCount: 1, Payload: []byte(`{}`), Source: "test"}}); err != nil {
		t.Fatal(err)
	}
	cov := compute.NewCoverage(s, []string{"solana"})
	if err := cov.Step(ctx); err != nil {
		t.Fatal(err)
	}
	ranges := func() string {
		return one(t, c, `SELECT string_agg(from_block || '-' || to_block || ' ' || to_char(from_time AT TIME ZONE 'UTC', 'HH24:MI:SS') || '-' ||
			to_char(to_time AT TIME ZONE 'UTC', 'HH24:MI:SS'), ', ' ORDER BY from_block) FROM coverage_ranges WHERE chain = 'solana'`)
	}
	if got, want := ranges(), "1-5 09:00:01-09:00:05, 7-7 09:00:07-09:00:07"; got != want {
		t.Errorf("coverage = %s; want %s", got, want)
	}
	// Once 6 is parsed the two ranges merge.
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: 6, Time: at(6), Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := cov.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := ranges(), "1-7 09:00:01-09:00:07"; got != want {
		t.Errorf("coverage after parsing 6 = %s; want %s", got, want)
	}
}
