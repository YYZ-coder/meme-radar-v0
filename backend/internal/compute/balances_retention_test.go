package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestBalancesSurviveRawAndLedgerRetention(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	old := day.AddDate(0, 0, -40)
	apply(t, s, "bsc", 1, old, parse.Facts{})
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 1, Time: old, Version: 1, Deltas: []parse.Balance{{Token: []byte("T"), Owner: []byte("A"), Delta: "100"}}}}); err != nil {
		t.Fatal(err)
	}
	b := compute.NewBalances(s, []string{"bsc"})
	if err := b.Step(ctx, day); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, day.AddDate(0, 0, -30), day.AddDate(0, 0, -30)); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, "SELECT count(*)::text FROM balance_deltas"); got != "0" {
		t.Errorf("expired deltas=%s;want 0", got)
	}
	if got := one(t, c, "SELECT count(*)::text FROM balance_journal"); got != "0" {
		t.Errorf("expired journal=%s;want 0", got)
	}
	if err := b.Step(ctx, day.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, "SELECT balance::text FROM token_balances"); got != "100" {
		t.Errorf("retention retracted historical balance=%s;want 100", got)
	}
}
