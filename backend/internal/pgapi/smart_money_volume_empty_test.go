package pgapi_test

import (
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
)

// A positive holder with complete 24h collection and no trades has two known
// zero amounts, not two unknown amounts. Balance presence is independent.
func TestPgTokenSmartMoneyVolumesNoTrades(t *testing.T) {
	f := smartMoneyVolumeFixture(t)
	for i, at := range []time.Time{f.now.Add(-24 * time.Hour), f.now.Add(-23 * time.Hour), f.now.Add(-time.Minute), f.now} {
		f.apply(t, "bsc", uint64(i+2), at, parse.Facts{})
	}
	if err := f.st.RefreshSmTrades(t.Context(), "bsc", f.now); err != nil {
		t.Fatal(err)
	}
	var got api.TokenSmartMoney
	getPG(t, f.handler(), "/api/v1/tokens/{chain}/{address}/smart-money", holdingsToken+"/smart-money", 200, &got)
	if len(got.Items) != 1 {
		t.Fatalf("positive holder=%+v", got)
	}
	row := got.Items[0]
	if row.BuyVolumeUSD == nil || *row.BuyVolumeUSD != "0" || row.SellVolumeUSD == nil || *row.SellVolumeUSD != "0" {
		t.Errorf("no trades with complete coverage: buy=%v sell=%v; want 0/0", row.BuyVolumeUSD, row.SellVolumeUSD)
	}
}
