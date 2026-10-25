package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestBalancesConcurrentReparseDoesNotDoubleCount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, c := open(t)
	at := day.Add(time.Hour)
	apply(t, s, "bsc", 1, at, parse.Facts{})
	apply(t, s, "bsc", 2, at.Add(time.Second), parse.Facts{})
	block := func(n uint64, ds ...parse.Balance) store.BalanceBlock {
		return store.BalanceBlock{Chain: "bsc", Num: n, Time: at.Add(time.Duration(n-1) * time.Second), Version: 1, Deltas: ds}
	}
	delta := func(owner, amount string) parse.Balance {
		return parse.Balance{Token: []byte("T"), Owner: []byte(owner), Delta: amount}
	}
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{block(1, delta("A", "100"))}); err != nil {
		t.Fatal(err)
	}
	b := compute.NewBalances(s, []string{"bsc"})
	errc := make(chan error, 2)
	go func() {
		for range 40 {
			for _, ds := range [][]parse.Balance{{delta("A", "-30"), delta("C", "30")}, {delta("A", "-10"), delta("C", "10")}} {
				if err := s.ApplyBalances(ctx, []store.BalanceBlock{block(2, ds...)}); err != nil {
					errc <- err
					return
				}
			}
		}
		errc <- nil
	}()
	go func() {
		for range 40 {
			if err := b.Step(ctx, at.Add(time.Minute)); err != nil {
				errc <- err
				return
			}
		}
		errc <- nil
	}()
	for range 2 {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{block(2, delta("A", "-7"), delta("C", "7"))}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := b.Step(ctx, at.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if got := one(t, c, "SELECT string_agg(convert_from(owner,'UTF8')||':'||balance,',' ORDER BY owner) FROM token_balances"); got != "A:93,C:7" {
		t.Errorf("concurrent reparse result=%s;want A:93,C:7", got)
	}
}
