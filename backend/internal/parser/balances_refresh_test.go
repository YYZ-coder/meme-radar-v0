package parser_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parser"
	"github.com/meme-radar/radar/internal/store"
)

func TestBalancePassRefreshesTrackedTokensEveryBatch(t *testing.T) { tokenTrackingScenario(t, false) }

func TestBalancePassTracksBackfilledTokenBeforeNewerBlock(t *testing.T) {
	tokenTrackingScenario(t, true)
}

func tokenTrackingScenario(t *testing.T, backfill bool) {
	ctx := context.Background()
	s, c := setup(t)
	token := evm.MustAddress("0x0000000000000000000000000000000000000001")
	old := evm.MustAddress("0x0000000000000000000000000000000000000002")
	transfer := fmt.Sprintf(`{"block":{"timestamp":"0x1","transactions":[{"hash":"0x%064x","from":"0x%040x","to":"0x%040x","value":"0x0"}]},"receipts":[{"transactionHash":"0x%064x","status":"0x1","logs":[{"address":"%s","topics":["0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef","0x%064x","0x%064x"],"data":"0x%064x","logIndex":"0x0"}]}]}`, 1, 1, 2, 1, token.Hex(), 0, 3, 100)
	at := func(n uint64) time.Time { return time.Unix(1792719900+int64(n), 0) }
	commit := func(n uint64, main bool, f parse.Facts) {
		t.Helper()
		if err := s.Commit(ctx, store.Commit{Chain: "bsc", Block: store.Block{Num: n, Hash: []byte{byte(n)}, Time: at(n), Status: store.StatusConfirmed, Payload: []byte(transfer), Source: "test"}}); err != nil {
			t.Fatal(err)
		}
		if main {
			if err := s.ApplyParsed(ctx, store.Parsed{Chain: "bsc", Num: n, Time: at(n), Version: 7, Facts: f}); err != nil {
				t.Fatal(err)
			}
		}
	}
	initial := uint64(1)
	if backfill {
		initial = 100
	}
	commit(initial, true, parse.Facts{Tokens: []parse.Token{{Address: old[:]}}})
	pass := parser.NewBalances(parser.BalanceConfig{Chain: "bsc", Family: "evm", Store: s, Batch: 1})
	if _, err := pass.Step(ctx); err != nil {
		t.Fatal(err)
	}
	// Token T becomes tracked in block 2. A newer block 3 is claimed first;
	// waiting 60s would miss T in block 3 permanently. Block 4 is unparsed.
	commit(2, true, parse.Facts{Tokens: []parse.Token{{Address: token[:], Created: true}}})
	commit(3, true, parse.Facts{})
	commit(4, false, parse.Facts{})
	if _, err := pass.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if got := query(t, c, "SELECT coalesce(max(balance_version),0)::text FROM chain_blocks WHERE block_num=4"); got != "0" {
		t.Errorf("unparsed block claimed: %s", got)
	}
	if got := query(t, c, "SELECT coalesce(sum(delta),0)::text FROM balance_deltas WHERE block_num=3"); got != "100" {
		t.Errorf("new token next batch delta=%s;want 100", got)
	}
	if _, err := pass.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if got := query(t, c, "SELECT coalesce(sum(delta),0)::text FROM balance_deltas WHERE block_num=2"); got != "100" {
		t.Errorf("new token creation block delta=%s;want 100", got)
	}
	commit(2, false, parse.Facts{})
	if _, err := pass.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if got := query(t, c, "SELECT coalesce(balance_version,0)::text FROM chain_blocks WHERE block_num=2"); got != "0" {
		t.Errorf("rewrite was claimed before main reparse: %s", got)
	}
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: "bsc", Num: 2, Time: at(2), Version: 8}); err != nil {
		t.Fatal(err)
	}
	if _, err := pass.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if got := query(t, c, "SELECT coalesce(sum(delta),0)::text FROM balance_deltas WHERE block_num=2"); got != "100" {
		t.Errorf("rewrite after main parse delta=%s;want 100", got)
	}

}
