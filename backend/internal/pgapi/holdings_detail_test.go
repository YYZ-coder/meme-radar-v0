package pgapi_test

import (
	"testing"

	"github.com/meme-radar/radar/internal/api"
)

// An empty published H3 version and known balances mean zero smart holders.
// H4 buy/sell volumes remain unavailable; no draft tables are created here.
func TestPgTokenSmartMoneyAndCreatorEmptyActiveList(t *testing.T) {
	f := holdingsFixture(t)
	h := f.handler()
	for _, suffix := range []string{"", "?currency=native"} {
		var got api.TokenDetail
		getPG(t, h, "/api/v1/tokens/{chain}/{address}", holdingsToken+suffix, 200, &got)
		if got.Holders == nil || *got.Holders != 4 || got.HolderCoverage.Status != "full" || got.HolderCoverage.Since == nil || *got.HolderCoverage.Since != "2026-10-23T09:54:00.000Z" {
			t.Errorf("holder state=%+v", got)
		}
		// Six-decimal atomic balance 2e13 => 2e7 whole / 1e9 supply *100 = 2%.
		if got.Creator == nil || got.Creator.HoldingPct == nil || *got.Creator.HoldingPct != "2" {
			t.Errorf("creator=%+v, want holding_pct 2", got.Creator)
		}
		if got.SMHolders == nil || *got.SMHolders != 0 {
			t.Errorf("empty active list: sm_holders=%v; want 0", got.SMHolders)
		}
		if got.ATHSince == nil || *got.ATHSince != "2026-10-23T09:54:00.000Z" {
			t.Errorf("ATH since=%v", got.ATHSince)
		}
	}
	var got api.TokenSmartMoney
	getPG(t, h, "/api/v1/tokens/{chain}/{address}/smart-money", holdingsToken+"/smart-money", 200, &got)
	if got.ListVersion != "1" || (got.Holders == nil || *got.Holders != 0) || got.Items == nil || len(got.Items) != 0 {
		t.Errorf("known empty active list: %+v", got)
	}
}
