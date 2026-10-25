package pgapi_test

import (
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/store"
)

// 00-common's missing-coverage rule: known observed trades do not make a
// complete 24h total. Reingest clears one main-parse mark inside that window.
func TestPgTokenSmartMoneyVolumesNeedCoverage(t *testing.T) {
	f := smartMoneyVolumeFixture(t)
	if err := f.st.Commit(t.Context(), store.Commit{Chain: "bsc", Block: store.Block{Num: 3, Hash: raw(32, 3), ParentHash: raw(32, 2), Time: f.now.Add(-23 * time.Hour), Status: store.StatusConfirmed, TxCount: 1, Payload: []byte(`{}`), Source: "test"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.RefreshCoverage(t.Context(), "bsc"); err != nil {
		t.Fatal(err)
	}
	var got api.TokenSmartMoney
	getPG(t, f.handler(), "/api/v1/tokens/{chain}/{address}/smart-money", holdingsToken+"/smart-money", 200, &got)
	if len(got.Items) != 1 {
		t.Fatalf("observed balance card disappeared: %+v", got)
	}
	row := got.Items[0]
	if row.BuyVolumeUSD != nil || row.SellVolumeUSD != nil {
		t.Errorf("24h with unparsed interior block must be NULL, got %+v", row)
	}
}
