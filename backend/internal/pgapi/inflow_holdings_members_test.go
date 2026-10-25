package pgapi_test

import (
	"net/url"
	"testing"

	"github.com/meme-radar/radar/internal/api"
)

// Regression: H3 activation changes smart-holder identity, not ordinary holder
// counts. Old paged snapshots retain their original numbers after rebuilding.
func TestPgInflowHoldingsFollowActiveMembership(t *testing.T) {
	f := inflowHoldingsFixture(t)
	h := f.handler()
	route := "/api/v1/smart-money/inflow"
	var old api.InflowPage
	getPG(t, h, route, route+"?chain=bsc&window=5m", 200, &old)
	if _, err := holderBackend(f).Exclude(t.Context(), api.BSC, "0x0000000000000000000000000000000000000011", "holdings regression"); err != nil {
		t.Fatal(err)
	}
	buildHolderMembers(t, f)
	getPG(t, h, route, route+"?chain=bsc&window=5m", 503, nil)
	if _, err := f.st.RebuildInflow(t.Context(), "bsc", "5m", f.now); err != nil {
		t.Fatal(err)
	}
	var fresh api.InflowPage
	getPG(t, h, route, route+"?chain=bsc&window=5m", 200, &fresh)
	if fresh.ListVersion != "3" || fresh.Total != 3 {
		t.Fatalf("fresh page=%+v", fresh)
	}
	for _, row := range fresh.Items {
		want := int64(1)
		if row.Token.Address == "0x0000000000000000000000000000000000000061" {
			want = 3
		}
		if row.Holders == nil || *row.Holders != want || row.SMHolders == nil || *row.SMHolders != 0 {
			t.Errorf("fresh counts=%+v; want %d ordinary, zero smart", row, want)
		}
	}
	var frozen api.InflowPage
	getPG(t, h, route, route+"?query_id="+url.QueryEscape(old.QueryID)+"&offset=0", 200, &frozen)
	for _, row := range frozen.Items {
		if row.SMHolders == nil || *row.SMHolders != 1 {
			t.Errorf("frozen counts changed=%+v", row)
		}
	}
	for _, chain := range []string{"base", "solana"} {
		if _, err := f.st.RebuildInflow(t.Context(), chain, "5m", f.now); err != nil {
			t.Fatal(err)
		}
	}
	var all api.InflowPage
	getPG(t, h, route, route+"?window=5m", 200, &all)
	if all.Total != 4 {
		t.Fatalf("all chain holdings page=%+v", all)
	}
	for _, row := range all.Items {
		if row.Token.Chain == api.Base && (row.Holders != nil || row.SMHolders != nil) {
			t.Errorf("unknown Base balances must stay NULL=%+v", row)
		}
	}
}
