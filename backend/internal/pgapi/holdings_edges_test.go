package pgapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/balances"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

// Regression supplements: no claim of a new TDD Red for already-green guards.
func TestPgHoldingsCoverageAndUnknowns(t *testing.T) {
	f := holdingsFixture(t)
	ctx := context.Background()
	// A pending block after creation turns full into partial on the next round.
	f.apply(t, "bsc", 100, apiTime.Add(time.Second), parse.Facts{})
	if err := f.st.RefreshBalances(ctx, "bsc", f.now, balances.Version); err != nil {
		t.Fatal(err)
	}
	h := f.handler()
	var holders api.Holders
	getPG(t, h, holdersRoute, holdingsToken+"/holders?limit=1", 200, &holders)
	if holders.Coverage.Status != "partial" || holders.Total == nil || *holders.Total != 4 {
		t.Errorf("observed count/coverage=%+v", holders)
	}
	var detail api.TokenDetail
	getPG(t, h, "/api/v1/tokens/{chain}/{address}", holdingsToken, 200, &detail)
	if !reflect.DeepEqual(detail.HolderCoverage, holders.Coverage) || detail.Holders == nil || *detail.Holders != 4 {
		t.Errorf("detail/tab states=%+v / %+v", detail.HolderCoverage, holders.Coverage)
	}
	// Missing circulating supply is NULL, and a known zero is also undefined.
	// Use the formal parse + UpdateMarket path, never mutate compute projections.
	six := int16(6)
	f.apply(t, "bsc", 1, apiTime.Add(-6*time.Minute), parse.Facts{Tokens: []parse.Token{{Address: raw(20, 0x61), Created: true, Decimals: &six, TotalSupply: "0", Creator: raw(20, 0xee)}}})
	if err := f.st.UpdateMarket(ctx, "bsc", f.now, false); err != nil {
		t.Fatal(err)
	}
	getPG(t, h, holdersRoute, holdingsToken+"/holders?limit=1", 200, &holders)
	if len(holders.Items) != 1 || holders.Items[0].SharePct != nil {
		t.Errorf("zero denominator must be null: %+v", holders)
	}
	getPG(t, h, "/api/v1/tokens/{chain}/{address}", holdingsToken, 200, &detail)
	if detail.Creator == nil || detail.Creator.HoldingPct != nil {
		t.Errorf("zero denominator creator=%+v", detail.Creator)
	}

}

func TestPgHolderCursorBoundaries(t *testing.T) {
	f := holdingsFixture(t)
	h := f.handler()
	var got api.Holders
	getPG(t, h, holdersRoute, holdingsToken+"/holders?limit=2", 200, &got)
	if got.NextCursor == nil {
		t.Fatal("fixture needs a page boundary")
	}
	cursor := url.QueryEscape(*got.NextCursor)
	getPG(t, h, holdersRoute, holdingsToken+"/holders?label=smart_money&cursor="+cursor, 400, nil)
	getPG(t, h, holdersRoute, "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000062/holders?cursor="+cursor, 400, nil)
	var sm api.Holders
	getPG(t, h, holdersRoute, holdingsToken+"/holders?label=smart_money", 200, &sm)
	if sm.Total == nil || *sm.Total != 0 || len(sm.Items) != 0 {
		t.Errorf("empty active filter must have known total zero: %+v", sm)
	}
}

func TestPgNativeLookupHonorsRateAndDeadline(t *testing.T) {
	f := holdingsFixture(t)
	var calls atomic.Int64
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0xde0b6b3a7640000"}`)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(rpc.Close)
	for i := range f.cfg.Chains {
		if f.cfg.Chains[i].Name == "bsc" {
			f.cfg.Chains[i].LookupRPCURL = rpc.URL
			f.cfg.Chains[i].LookupRPS = 0.001
		}
	}
	// One token is available immediately; another is 1000 seconds away, beyond
	// this bounded request. Failure to reserve it must leave only that value NULL.
	h := httpapi.New(pgapi.New(f.st, f.cfg, func() time.Time { return f.now }), httpapi.Options{QueryTimeout: time.Second, Now: func() time.Time { return f.now }})
	var got api.Holders
	getPG(t, h, holdersRoute, holdingsToken+"/holders?limit=2", 200, &got)
	if len(got.Items) != 2 || got.Items[0].NativeBalance == nil || *got.Items[0].NativeBalance != "1" || got.Items[1].NativeBalance != nil || calls.Load() != 1 {
		t.Errorf("rate/deadline=%+v, calls=%d", got, calls.Load())
	}
}

func TestPgMissingHolderSupply(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	six := int16(6)
	f.apply(t, "bsc", 1, apiTime, parse.Facts{Tokens: []parse.Token{{Address: raw(20, 0x61), Created: true, Decimals: &six, Creator: raw(20, 0xee)}}})
	if err := f.st.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "bsc", Num: 1, Time: apiTime, Version: balances.Version, Deltas: []parse.Balance{{Token: raw(20, 0x61), Owner: raw(20, 0xee), Delta: "1000000"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.RefreshBalances(ctx, "bsc", f.now, balances.Version); err != nil {
		t.Fatal(err)
	}
	f.cfg.Chains = nil // no external RPC in this unknown-supply scenario
	h := f.handler()
	var got api.Holders
	getPG(t, h, holdersRoute, holdingsToken+"/holders", 200, &got)
	if len(got.Items) != 1 || got.Items[0].Amount != "1" || got.Items[0].SharePct != nil {
		t.Errorf("missing supply=%+v", got)
	}
	var detail api.TokenDetail
	getPG(t, h, "/api/v1/tokens/{chain}/{address}", holdingsToken, 200, &detail)
	if detail.Creator == nil || detail.Creator.HoldingPct != nil {
		t.Errorf("missing supply creator=%+v", detail.Creator)
	}
}
