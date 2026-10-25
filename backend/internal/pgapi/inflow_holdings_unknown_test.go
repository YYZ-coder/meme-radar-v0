package pgapi_test

import (
	"testing"

	"github.com/meme-radar/radar/internal/api"
)

// Missing developer balance is unknown per the frozen holding_pct rule.
// It must not be conflated with B's explicitly observed zero balance.
func TestPgInflowUnknownDeveloperIsNotSold(t *testing.T) {
	f := inflowHoldingsFixture(t)
	var got api.InflowPage
	route := "/api/v1/smart-money/inflow"
	getPG(t, f.handler(), route, route+"?chain=bsc&window=5m&dev_status=sold", 200, &got)
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].Token.Address != "0x0000000000000000000000000000000000000062" {
		t.Errorf("sold filter=%+v; want only B with explicit zero, never C with missing balance", got)
	}
	projection, err := holderBackend(f).InflowProjection(t.Context(), api.BSC, "5m", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range projection.Rows {
		if v.Row.Token.Address == "0x0000000000000000000000000000000000000063" && v.DevHoldingPct != nil {
			t.Errorf("C DevHoldingPct=%v; want NULL for missing balance", v.DevHoldingPct)
		}
	}
}
