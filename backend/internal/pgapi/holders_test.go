package pgapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/balances"
	"github.com/meme-radar/radar/internal/store"
)

const holdersRoute = "/api/v1/tokens/{chain}/{address}/holders"
const holdingsToken = "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061"

// Hand-calculated whole units: supply 1e9; dev 2e7, A/B 1e7 each,
// C 5e6 => 2%, 1%, 1%, 0.5%. Atomic quantities use six decimals.
// Balances and coverage are built through the real parse/compute writers.
func holdingsFixture(t *testing.T) *fixture {
	t.Helper()
	f := detailFixture(t, false)
	ctx := context.Background()
	blocks, err := f.st.UnbalancedBlocks(ctx, "bsc", balances.Version, 100)
	if err != nil {
		t.Fatal(err)
	}
	dead := evm.MustAddress("0x000000000000000000000000000000000000dead")
	ds := []parse.Balance{
		{Owner: raw(20, 0xee), Delta: "20000000000000"},
		{Owner: raw(20, 0x11), Delta: "10000000000000"},
		{Owner: raw(20, 0x12), Delta: "10000000000000"},
		{Owner: raw(20, 0x13), Delta: "5000000000000"},
		{Owner: raw(20, 0), Delta: "1000000"}, {Owner: dead[:], Delta: "1000000"},
		{Owner: raw(20, 0x14), Delta: "-7"}, {Owner: raw(20, 0x15), Delta: "0"},
	}
	for i := range ds {
		ds[i].Token = raw(20, 0x61)
	}
	for _, block := range blocks {
		b := store.BalanceBlock{Chain: "bsc", Num: block.Num, Time: block.Time, Version: balances.Version}
		if block.Num == 1 {
			b.Deltas = ds
		}
		if err := f.st.ApplyBalances(ctx, []store.BalanceBlock{b}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.st.RefreshBalances(ctx, "bsc", f.now, balances.Version); err != nil {
		t.Fatal(err)
	}
	// All RPC use in these tests is local and bounded; this slice checks the DB.
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	t.Cleanup(rpc.Close)
	for i := range f.cfg.Chains {
		f.cfg.Chains[i].LookupRPCURL = rpc.URL
		f.cfg.Chains[i].LookupRPS = 1000
	}
	return f
}

func TestPgHoldersTab(t *testing.T) {
	f := holdingsFixture(t)
	smartLabelMembers(t, f)
	h := f.handler()
	cursor := ""
	for page, wantOwners := range [][]byte{{0xee, 0x11}, {0x12, 0x13}} {
		var got api.Holders
		target := holdingsToken + "/holders?limit=2" + cursor
		getPG(t, h, holdersRoute, target, 200, &got)
		if got.Total == nil || *got.Total != 4 || got.Coverage.Status != "full" || got.Coverage.Since == nil || *got.Coverage.Since != "2026-10-23T09:54:00.000Z" {
			t.Errorf("total/coverage=%+v", got)
		}
		if len(got.Items) != 2 {
			t.Fatalf("page %d items=%+v, want 2", page, got.Items)
		}
		for i, row := range got.Items {
			index := page*2 + i
			if row.Wallet.Address != "0x00000000000000000000000000000000000000"+[]string{"ee", "11", "12", "13"}[index] || row.Rank != int64(index+1) {
				t.Errorf("page %d row=%+v, owner tail=%x", page, row, wantOwners[i])
			}
			if row.Amount != []string{"20000000", "10000000", "10000000", "5000000"}[index] || row.SharePct == nil || *row.SharePct != []string{"2", "1", "1", "0.5"}[index] {
				t.Errorf("amount/share=%+v", row)
			}
			labels := []string{}
			if index == 0 {
				labels = []string{"dev", "smart_money"}
			} else if index == 1 {
				labels = []string{"smart_money"}
			}
			if !reflect.DeepEqual(row.Wallet.Labels, labels) {
				t.Errorf("labels=%v, want %v", row.Wallet.Labels, labels)
			}
			if row.AvgBuyPriceUSD != nil || row.AvgSellPriceUSD != nil || row.RealizedPnLUSD != nil || row.UnrealizedPnLUSD != nil {
				t.Errorf("M2 values must be null: %+v", row)
			}
		}
		if page == 0 {
			if got.NextCursor == nil {
				t.Fatal("missing next cursor")
			}
			cursor = "&cursor=" + url.QueryEscape(*got.NextCursor)
		} else if got.NextCursor != nil {
			t.Errorf("final cursor=%v", got.NextCursor)
		}
	}
	getPG(t, h, holdersRoute, holdingsToken+"/holders?cursor=bogus", 400, nil)
}
