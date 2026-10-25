package httpapi_test

import (
	"math"
	"net/http"
	"strconv"
	"testing"
)

type overview struct {
	Window        string
	TrackedTokens *int64    `json:"tracked_tokens"`
	BuyVolume     *string   `json:"buy_volume_usd"`
	SellVolume    *string   `json:"sell_volume_usd"`
	BuyShare      *string   `json:"buy_share_pct"`
	InflowBars    []*string `json:"inflow_bars"`
}

func f(s *string) float64 { v, _ := strconv.ParseFloat(*s, 64); return v }

// Data model §8.1: tracked_tokens for all chains is the sum of the three chains;
// buy_share_pct = buy ÷ (buy + sell) × 100; inflow_bars has 16 buckets.
func TestOverview(t *testing.T) {
	h := newHarness(t)
	const route = "/api/v1/smart-money/overview"
	var all overview
	h.get(route, route+"?window=1h", http.StatusOK, &all)
	if all.Window != "1h" || len(all.InflowBars) != 16 || all.TrackedTokens == nil {
		t.Fatalf("overview = %+v, want window 1h, 16 bars, tracked tokens", all)
	}
	var sum int64
	for _, c := range []string{"solana", "base", "bsc"} {
		var o overview
		h.get(route, route+"?window=1h&chain="+c, http.StatusOK, &o)
		sum += *o.TrackedTokens
	}
	if sum != *all.TrackedTokens {
		t.Errorf("tracked_tokens all = %d, sum of chains = %d", *all.TrackedTokens, sum)
	}
	buy, sell := f(all.BuyVolume), f(all.SellVolume)
	if want := buy / (buy + sell) * 100; math.Abs(f(all.BuyShare)-want) > 1e-3 {
		t.Errorf("buy_share_pct = %s, want %.6f", *all.BuyShare, want)
	}
	var e errorBody
	h.get(route, route+"?window=2h", http.StatusBadRequest, &e)
	if e.Error.Field == nil || *e.Error.Field != "window" {
		t.Errorf("bad window error field = %v, want window", e.Error.Field)
	}
}

func TestFacetsFollowChain(t *testing.T) {
	h := newHarness(t)
	const route = "/api/v1/smart-money/inflow/facets"
	var body struct {
		Dexes, Platforms []struct{ ID, Chain string }
	}
	h.get(route, route+"?chain=base&window=24h", http.StatusOK, &body)
	if len(body.Dexes) == 0 || len(body.Platforms) == 0 {
		t.Fatalf("base facets empty: %+v", body)
	}
	for _, x := range append(body.Dexes, body.Platforms...) {
		if x.Chain != "base" {
			t.Errorf("facet %s chain = %s, want base", x.ID, x.Chain)
		}
	}
}
