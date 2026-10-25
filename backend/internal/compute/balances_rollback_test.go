package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestBalancesRetractRollbackAndPendingRewrite(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	at := day.Add(time.Hour)
	delta := func(owner, amount string) parse.Balance {
		return parse.Balance{Token: []byte("T"), Owner: []byte(owner), Delta: amount}
	}
	write := func(n uint64, ds ...parse.Balance) {
		t.Helper()
		apply(t, s, "bsc", n, at.Add(time.Duration(n)*time.Second), parse.Facts{})
		if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: n, Time: at.Add(time.Duration(n) * time.Second), Version: 1, Deltas: ds}}); err != nil {
			t.Fatal(err)
		}
	}
	b := compute.NewBalances(s, []string{"bsc"})
	check := func(want string) {
		t.Helper()
		if err := b.Step(ctx, at.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if got := one(t, c, "SELECT string_agg(convert_from(owner,'UTF8')||':'||balance,',' ORDER BY owner) FROM token_balances"); got != want {
			t.Errorf("balances=%s;want %s", got, want)
		}
	}
	write(1, delta("A", "100"))
	write(2, delta("A", "-30"), delta("C", "30"))
	check("A:70,C:30")
	if err := s.Rollback(ctx, "bsc", 1); err != nil {
		t.Fatal(err)
	}
	check("A:100,C:0")
	write(2, delta("A", "-10"), delta("C", "10"))
	check("A:90,C:10")
	if err := s.Commit(ctx, store.Commit{Chain: "bsc", Block: store.Block{Num: 2, Time: at.Add(2 * time.Second), Hash: []byte{99}, Status: store.StatusConfirmed, Payload: []byte(`{}`), Source: "test"}}); err != nil {
		t.Fatal(err)
	}
	check("A:100,C:0")
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "bsc", Num: 2, Time: at.Add(2 * time.Second), Version: 1}); err != nil {
		t.Fatal(err)
	}
	check("A:100,C:0")
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 2, Time: at.Add(2 * time.Second), Version: 1, Deltas: []parse.Balance{delta("A", "-20"), delta("C", "20")}}}); err != nil {
		t.Fatal(err)
	}
	check("A:80,C:20")
}
