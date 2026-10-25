package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestBalancesAccumulateAndFollowReparse(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	token := []byte("T")
	at := func(n uint64) time.Time { return day.Add(time.Duration(n) * time.Second) }
	delta := func(owner, amount string) parse.Balance {
		return parse.Balance{Token: token, Owner: []byte(owner), Delta: amount}
	}
	write := func(n uint64, ds ...parse.Balance) {
		t.Helper()
		apply(t, s, "bsc", n, at(n), parse.Facts{Tokens: []parse.Token{{Address: token}}})
		if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: n, Time: at(n), Version: 1, Deltas: ds}}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want string) {
		t.Helper()
		got := one(t, c, "SELECT coalesce(string_agg(convert_from(owner,'UTF8')||':'||balance,',' ORDER BY owner),'') FROM token_balances")
		if got != want {
			t.Errorf("balances=%s;want %s", got, want)
		}
	}
	write(1, delta("A", "100"), delta("B", "50"))
	write(2, delta("A", "-30"), delta("C", "30"))
	b := compute.NewBalances(s, []string{"bsc"})
	if err := b.Step(ctx, day.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	check("A:70,B:50,C:30")
	if got := one(t, c, "SELECT count(*)::text FROM token_balances WHERE balance>0"); got != "3" {
		t.Errorf("holders=%s;want 3", got)
	}
	if got := one(t, c, "SELECT count(*)::text FROM balance_journal"); got != "4" {
		t.Errorf("journal rows=%s;want 4", got)
	}
	if err := b.Step(ctx, day.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	check("A:70,B:50,C:30")
	// Direct balance reparse, without ingest rewriting main facts.
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 2, Time: at(2), Version: 2, Deltas: []parse.Balance{delta("A", "-10"), delta("C", "10")}}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Step(ctx, day.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	check("A:90,B:50,C:10")
	write(0, delta("B", "5"))
	if err := b.Step(ctx, day.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	check("A:90,B:55,C:10")
	if got := one(t, c, "SELECT (at=(SELECT max(balance_parsed_at) FROM chain_blocks WHERE chain='bsc'))::text FROM compute_marks WHERE chain='bsc' AND name='balances'"); got != "true" {
		t.Errorf("balance watermark=%s;want true", got)
	}
	// Reparse to no facts retracts all of that block's old changes.
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 2, Time: at(2), Version: 3}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Step(ctx, day.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	check("A:100,B:55,C:0")
}
