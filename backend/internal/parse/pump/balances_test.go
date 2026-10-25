package pump_test

import (
	"os"
	"slices"
	"testing"

	"encoding/json"

	"github.com/meme-radar/radar/internal/parse/balances"
	"github.com/meme-radar/radar/internal/solana"
)

func TestPumpEmitsBalanceDeltas(t *testing.T) {
	// Expected deltas are computed from RPC meta by expect_balances.py.
	data, err := os.ReadFile("../../../testdata/expect/h5-sol-deltas.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]struct{ Tracked, Deltas []string }
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	for _, s := range samples(t) {
		t.Run(s.Name, func(t *testing.T) {
			tx, err := solana.ParseTx(s.Index, s.Tx)
			if err != nil {
				t.Fatal(err)
			}
			want := expected[s.Name]
			tracked := map[string]struct{}{}
			for _, address := range want.Tracked {
				k := solana.MustKey(address)
				tracked[string(k[:])] = struct{}{}
			}
			var got []string
			for _, b := range balances.Solana(tx, tracked) {
				if b.Tx != s.Index || b.Ix != 0 {
					t.Fatalf("position = %d/%d; want %d/0", b.Tx, b.Ix, s.Index)
				}
				got = append(got, solana.EncodeBase58(b.Owner)+"|"+solana.EncodeBase58(b.Token)+"|"+b.Delta)
			}
			slices.Sort(got)
			if !slices.Equal(got, want.Deltas) {
				t.Errorf("deltas = %v; want %v", got, want.Deltas)
			}
			if got := balances.Solana(tx, nil); len(got) != 0 {
				t.Errorf("untracked mints emitted: %v", got)
			}
			tx.Failed = true
			if got := balances.Solana(tx, tracked); len(got) != 0 {
				t.Errorf("failed tx emitted: %v", got)
			}
		})
	}
}
