package pgapi_test

import (
	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"testing"
	"time"
)

// H4-11 uses the same formal facts as H4-10: Pancake A=$1+$2 and C=$5
// ($8, two DISTINCT tokens); Four.meme B=$4; Base Clanker D=$7.
func TestPgFacets(t *testing.T) {
	f := inflowFixture(t, true)
	f.cfg.DisplayNames.Dexes["pancakeswap_v2"] = "Configured Pancake"
	delete(f.cfg.DisplayNames.Dexes, "four_meme_curve")
	h := f.handler()
	route := "/api/v1/smart-money/inflow/facets"
	var got api.Facets
	getPG(t, h, route, route+"?chain=bsc&window=5m", 200, &got)
	if got.Coverage != "complete" || len(got.Dexes) != 2 || len(got.Platforms) != 1 {
		t.Fatalf("facets=%+v; want complete, two dexes and one platform", got)
	}
	assertFacet := func(items []api.Facet, chain api.Chain, id, name, volume string, count int64) {
		t.Helper()
		for _, v := range items {
			if v.Chain == chain && v.ID == id {
				if v.Name != name || v.VolumeUSD == nil || *v.VolumeUSD != volume || v.TokenCount == nil || *v.TokenCount != count || v.ObservedTokenCount != count {
					t.Errorf("facet=%+v; want name=%s volume=%s count=%d", v, name, volume, count)
				}
				return
			}
		}
		t.Errorf("missing %s/%s in %+v", chain, id, items)
	}
	assertFacet(got.Dexes, api.BSC, "pancakeswap_v2", "Configured Pancake", "8", 2)
	assertFacet(got.Dexes, api.BSC, "four_meme_curve", "four_meme_curve", "4", 1)
	assertFacet(got.Platforms, api.BSC, "four_meme", "Four.meme", "12", 3)
	getPG(t, h, route, route+"?window=5m", 200, &got)
	if got.Coverage != "complete" || len(got.Dexes) != 3 || len(got.Platforms) != 2 {
		t.Fatalf("all facets=%+v", got)
	}
	assertFacet(got.Dexes, api.Base, "uniswap_v4", "Uniswap v4", "7", 1)
	assertFacet(got.Platforms, api.Base, "clanker", "Clanker", "7", 1)
	var page api.InflowPage
	listRoute := "/api/v1/smart-money/inflow"
	getPG(t, h, listRoute, listRoute+"?chain=bsc&window=5m&dex=pancakeswap_v2", 200, &page)
	if len(page.Items) != 2 || len(page.Items[0].Dexes) != 1 || page.Items[0].Dexes[0] != "Configured Pancake" {
		t.Fatalf("list display names=%+v; filter must retain raw IDs", page.Items)
	}
}

// Regression coverage for D140: observed counts remain available under a gap.
func TestPgFacetsPartialKeepsOnlyObservedCounts(t *testing.T) {
	f := inflowFixture(t, true)
	// Missing block 4 leaves a real coverage gap before the newer head.
	f.apply(t, "bsc", 5, apiTime.Add(time.Minute+time.Second), parse.Facts{})
	for _, err := range []error{f.st.RefreshStats(t.Context(), "bsc", f.now.Add(time.Minute)), f.st.RefreshSmTrades(t.Context(), "bsc", f.now.Add(time.Minute)), f.st.RefreshCoverage(t.Context(), "bsc")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.st.RebuildInflow(t.Context(), "bsc", "5m", f.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var got api.Facets
	route := "/api/v1/smart-money/inflow/facets"
	getPG(t, f.handler(), route, route+"?chain=bsc&window=5m", 200, &got)
	if got.Coverage != "partial" || len(got.Dexes) != 2 || len(got.Platforms) != 1 {
		t.Fatalf("partial facets=%+v", got)
	}
	for _, v := range append(got.Dexes, got.Platforms...) {
		if v.VolumeUSD != nil || v.TokenCount != nil || v.ObservedTokenCount <= 0 {
			t.Errorf("partial facet=%+v; full values must be NULL and observed retained", v)
		}
	}
}

// Real HTTP overview assertions supplement the metric-document examples.
func TestPgOverviewUsesActiveFactsAcrossChains(t *testing.T) {
	f := inflowFixture(t, true)
	f.now = apiTime
	f.apply(t, "base", 4, apiTime.Add(time.Second), parse.Facts{})
	if err := f.st.RefreshCoverage(t.Context(), "base"); err != nil {
		t.Fatal(err)
	}
	var got api.Overview
	route := "/api/v1/smart-money/overview"
	getPG(t, f.handler(), route, route+"?window=5m", 200, &got)
	if got.Coverage != "complete" || got.ListVersion != "2" || got.ActiveWallets == nil || *got.ActiveWallets != 2 || got.NetInflowUSD == nil || *got.NetInflowUSD != "14" || got.BuySharePct == nil || *got.BuySharePct != "100" || got.TrackedTokens == nil || *got.TrackedTokens != 4 || got.NewTokens == nil || *got.NewTokens != 0 || got.NetInflowChangePct != nil {
		t.Fatalf("all-chain overview=%+v; want two wallets, net14, buy share100, tracked4, new0, previous unknown", got)
	}
	want := []string{"7", "0", "0", "7", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0"}
	for i, s := range want {
		if len(got.InflowBars) != 16 || got.InflowBars[i] == nil || *got.InflowBars[i] != s {
			t.Fatalf("bars=%v; want %v", got.InflowBars, want)
		}
	}
}
