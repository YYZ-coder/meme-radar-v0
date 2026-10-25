package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestHolderCoverageState(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	at := day.Add(time.Hour)
	token, partial, unknown := evm.Address{1}, evm.Address{2}, evm.Address{3}
	a, b, negative, zero := evm.Address{4}, evm.Address{5}, evm.Address{6}, evm.Address{}
	dead := evm.MustAddress("0x000000000000000000000000000000000000dead")
	apply(t, s, "bsc", 10, at, parse.Facts{Tokens: []parse.Token{{Address: token[:], Created: true}, {Address: partial[:]}, {Address: unknown[:]}}})
	apply(t, s, "bsc", 11, at.Add(time.Second), parse.Facts{})
	apply(t, s, "bsc", 12, at.Add(2*time.Second), parse.Facts{})
	ds := []parse.Balance{{Token: token[:], Owner: a[:], Delta: "100"}, {Token: token[:], Owner: zero[:], Delta: "50"}, {Token: token[:], Owner: dead[:], Delta: "20"}, {Token: token[:], Owner: negative[:], Delta: "-5"}, {Token: partial[:], Owner: a[:], Delta: "5"}}
	for _, p := range []store.BalanceBlock{{Chain: "bsc", Num: 10, Time: at, Version: 1, Deltas: ds}, {Chain: "bsc", Num: 11, Time: at.Add(time.Second), Version: 1}, {Chain: "bsc", Num: 12, Time: at.Add(2 * time.Second), Version: 1, Deltas: []parse.Balance{{Token: token[:], Owner: b[:], Delta: "30"}}}} {
		if err := s.ApplyBalances(ctx, []store.BalanceBlock{p}); err != nil {
			t.Fatal(err)
		}
	}
	cov := compute.NewCoverage(s, []string{"bsc"})
	holds := compute.NewBalances(s, []string{"bsc"})
	if err := cov.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if err := holds.Step(ctx, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	check := func(address []byte, want string) {
		t.Helper()
		got := one(t, c, `SELECT holders||'|'||coverage||'|'||coalesce(to_char(since AT TIME ZONE 'UTC','HH24:MI:SS'),'NULL') FROM token_holder_state WHERE chain='bsc' AND token_address=$1`, address)
		if got != want {
			t.Errorf("state %x=%s;want %s", address, got, want)
		}
	}
	check(token[:], "2|full|01:00:00")
	check(partial[:], "1|partial|01:00:00")
	check(unknown[:], "0|unknown|NULL")
	if got := one(t, c, "SELECT count(*)::text FROM token_balances WHERE balance<0"); got != "1" {
		t.Errorf("negative balances retained=%s;want 1", got)
	}
	if got := one(t, c, "SELECT bool_and(as_of=(SELECT at FROM compute_marks WHERE chain='bsc' AND name='balances'))::text FROM token_holder_state"); got != "true" {
		t.Errorf("holder as_of does not match balance watermark: %s", got)
	}
	// Pending history entirely before the creation block cannot downgrade T.
	if err := s.Commit(ctx, store.Commit{Chain: "bsc", Block: store.Block{Num: 9, Time: at.Add(-time.Second), Hash: []byte{9}, Status: store.StatusConfirmed, Payload: []byte(`{}`), Source: "test"}}); err != nil {
		t.Fatal(err)
	}
	if err := holds.Step(ctx, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	check(token[:], "2|full|01:00:00")
	// Missing balance version at creation+1, and then an outdated version,
	// each prevents full. Completing each case restores full next round.
	for _, version := range []int{-1, 0} {
		apply(t, s, "bsc", 11, at.Add(time.Second), parse.Facts{})
		if version >= 0 {
			if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 11, Time: at.Add(time.Second), Version: version}}); err != nil {
				t.Fatal(err)
			}
		}
		if err := cov.Step(ctx); err != nil {
			t.Fatal(err)
		}
		if err := holds.Step(ctx, at.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		check(token[:], "2|partial|01:00:00")
		if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 11, Time: at.Add(time.Second), Version: 1}}); err != nil {
			t.Fatal(err)
		}
		if err := holds.Step(ctx, at.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		check(token[:], "2|full|01:00:00")
	}
	if err := s.Commit(ctx, store.Commit{Chain: "bsc", Block: store.Block{Num: 11, Time: at.Add(time.Second), Hash: []byte{11}, Status: store.StatusConfirmed, Payload: []byte(`{}`), Source: "test"}}); err != nil {
		t.Fatal(err)
	}
	// Leave the prior main coverage projection intact: pending_max must catch
	// the main-unparsed rewrite even before coverage is rebuilt.
	if err := holds.Step(ctx, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	check(token[:], "2|partial|01:00:00")
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "bsc", Num: 11, Time: at.Add(time.Second), Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 11, Time: at.Add(time.Second), Version: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := holds.Step(ctx, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	check(token[:], "2|full|01:00:00")

	// A later range separated by a missing block makes coverage partial.
	apply(t, s, "bsc", 14, at.Add(4*time.Second), parse.Facts{})
	if err := s.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 14, Time: at.Add(4 * time.Second), Version: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := cov.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if err := holds.Step(ctx, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	check(token[:], "2|partial|01:00:00")
}
