package pgapi_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/balances"
	"github.com/meme-radar/radar/internal/store"
)

// Hand-counted from real balance facts: A has three holders, B/C one each;
// only owner 0x11 is an active same-chain member. A's developer owns 10/1000
// (1%), B has an explicit zero, C has no developer balance observation.
func inflowHoldingsFixture(t *testing.T) *fixture {
	t.Helper()
	f := inflowFixture(t, false)
	six := int16(6)
	f.apply(t, "bsc", 4, apiTime.Add(32*time.Second), parse.Facts{Tokens: []parse.Token{
		{Address: raw(20, 0x61), Creator: raw(20, 0xee), Decimals: &six},
		{Address: raw(20, 0x62), Creator: raw(20, 0xef), Decimals: &six},
		{Address: raw(20, 0x63), Creator: raw(20, 0xf0), Decimals: &six},
	}})
	ds := []parse.Balance{
		{Token: raw(20, 0x61), Owner: raw(20, 0xee), Delta: "10000000"},
		{Token: raw(20, 0x61), Owner: raw(20, 0x11), Delta: "100000000"},
		{Token: raw(20, 0x61), Owner: raw(20, 0x12), Delta: "20000000"},
		{Token: raw(20, 0x62), Owner: raw(20, 0xef), Delta: "0"},
		{Token: raw(20, 0x62), Owner: raw(20, 0x11), Delta: "5000000"},
		{Token: raw(20, 0x63), Owner: raw(20, 0x11), Delta: "8000000"},
	}
	blocks := []store.BalanceBlock{}
	for i, at := range []time.Time{apiTime.Add(-6 * time.Minute), apiTime.Add(-4 * time.Minute), apiTime.Add(31 * time.Second), apiTime.Add(32 * time.Second)} {
		b := store.BalanceBlock{Chain: "bsc", Num: uint64(i + 1), Time: at, Version: balances.Version}
		if i == 0 {
			b.Deltas = ds
		}
		blocks = append(blocks, b)
	}
	for _, err := range []error{
		f.st.ApplyBalances(t.Context(), blocks),
		f.st.RefreshBalances(t.Context(), "bsc", f.now, balances.Version),
		f.st.RefreshStats(t.Context(), "bsc", f.now),
		f.st.RefreshSmTrades(t.Context(), "bsc", f.now),
		f.st.RefreshCoverage(t.Context(), "bsc"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.st.RebuildInflow(t.Context(), "bsc", "5m", f.now); err != nil {
		t.Fatal(err)
	}
	return f
}

// H4 already wires the known holdings into its persisted projection. This is
// regression coverage, not a newly claimed Red/Green implementation cycle.
func TestPgInflowHoldingsFields(t *testing.T) {
	f := inflowHoldingsFixture(t)
	h := f.handler()
	route := "/api/v1/smart-money/inflow"
	for _, currency := range []string{"usd", "native"} {
		var got api.InflowPage
		getPG(t, h, route, route+"?chain=bsc&window=5m&currency="+currency, 200, &got)
		if got.Total != 3 {
			t.Fatalf("holdings page=%+v", got)
		}
		for _, row := range got.Items {
			want := int64(1)
			if row.Token.Address == "0x0000000000000000000000000000000000000061" {
				want = 3
			}
			if row.Holders == nil || *row.Holders != want || row.SMHolders == nil || *row.SMHolders != 1 {
				t.Errorf("holdings row=%+v; want holders=%d, smart=1", row, want)
			}
		}
		body, _ := json.Marshal(api.BatchRequest{QueryID: got.QueryID, ListVersion: got.ListVersion, Window: "5m", Currency: currency, Tokens: []api.TokenKey{{Chain: api.BSC, Address: "0x0000000000000000000000000000000000000061"}}})
		var batch api.Batch
		listRequest(t, h, "POST", "/api/v1/tokens/batch", "/api/v1/tokens/batch", string(body), "", 200, &batch)
		if len(batch.Items) != 1 || batch.Items[0].Holders == nil || *batch.Items[0].Holders != 3 || batch.Items[0].SMHolders == nil || *batch.Items[0].SMHolders != 1 {
			t.Errorf("batch holdings=%+v", batch)
		}
	}
	var filtered api.InflowPage
	getPG(t, h, route, route+"?chain=bsc&window=5m&min_holders=2&min_sm_holders=1&dev_status=holding", 200, &filtered)
	if filtered.Total != 1 || filtered.Items[0].Token.Address != "0x0000000000000000000000000000000000000061" {
		t.Errorf("holding filter=%+v; want A only", filtered)
	}
	data, err := holderBackend(f).InflowProjection(t.Context(), api.BSC, "5m", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range data.Rows {
		if v.Row.Token.Address == "0x0000000000000000000000000000000000000061" && (v.DevHoldingPct == nil || *v.DevHoldingPct != 1) {
			t.Errorf("A dev share=%v; want 1%%", v.DevHoldingPct)
		}
		if v.Row.Token.Address == "0x0000000000000000000000000000000000000062" && (v.DevHoldingPct == nil || *v.DevHoldingPct != 0) {
			t.Errorf("B dev share=%v; want known 0%%", v.DevHoldingPct)
		}
	}
}
