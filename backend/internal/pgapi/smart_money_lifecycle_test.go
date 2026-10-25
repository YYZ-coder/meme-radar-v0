package pgapi_test

import (
	"testing"

	"github.com/meme-radar/radar/internal/api"
)

// Regression supplement: public reads must follow the immutable active head,
// even though accepted draft management records change before activation.
func TestPgSmartMoneyFollowsListActivation(t *testing.T) {
	f := smartMoneyHoldingsFixture(t)
	b := holderBackend(f)
	h := f.handler()
	check := func(version string, count int64) {
		t.Helper()
		var card api.TokenSmartMoney
		getPG(t, h, "/api/v1/tokens/{chain}/{address}/smart-money", holdingsToken+"/smart-money", 200, &card)
		if card.ListVersion != version || card.Holders == nil || *card.Holders != count || int64(len(card.Items)) != count {
			t.Fatalf("card=%+v;want version %s,count %d", card, version, count)
		}
		var detail api.TokenDetail
		getPG(t, h, "/api/v1/tokens/{chain}/{address}", holdingsToken, 200, &detail)
		if detail.SMHolders == nil || *detail.SMHolders != count {
			t.Errorf("detail smart holders=%v;want %d", detail.SMHolders, count)
		}
		var holders api.Holders
		getPG(t, h, holdersRoute, holdingsToken+"/holders?label=smart_money", 200, &holders)
		if holders.Total == nil || *holders.Total != count || int64(len(holders.Items)) != count {
			t.Errorf("filtered holders=%+v;want %d", holders, count)
		}
	}
	check("3", 1)
	if _, err := b.RemoveMember(t.Context(), api.BSC, "0x0000000000000000000000000000000000000011"); err != nil {
		t.Fatal(err)
	}
	check("3", 1)
	buildHolderMembers(t, f)
	check("4", 0)
	queueHolderMembers(t, f, "chain,address,label\nbsc,0x0000000000000000000000000000000000000011,M1\n")
	check("4", 0)
	buildHolderMembers(t, f)
	check("5", 1)
}
