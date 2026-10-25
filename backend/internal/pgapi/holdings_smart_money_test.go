package pgapi_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/balances"
	"github.com/meme-radar/radar/internal/store"
)

func smartMoneyHoldingsFixture(t *testing.T) *fixture {
	t.Helper()
	f := holdingsFixture(t)
	// Six decimals: m1 1e8, m2 zero, non-member 5e8. Excluded and negative
	// members are extra guards; the developer keeps 2e7, i.e. 2% of supply 1e9.
	ds := []parse.Balance{
		{Owner: raw(20, 0x11), Delta: "100000000000000"},
		{Owner: raw(20, 0x12), Delta: "0"},
		{Owner: raw(20, 0x13), Delta: "500000000000000"},
		{Owner: raw(20, 0x14), Delta: "70000000000000"},
		{Owner: raw(20, 0x15), Delta: "-9"},
		{Owner: raw(20, 0xee), Delta: "20000000000000"},
	}
	for i := range ds {
		ds[i].Token = raw(20, 0x61)
	}
	if err := f.st.ApplyBalances(t.Context(), []store.BalanceBlock{{Chain: "bsc", Num: 1, Time: apiTime.Add(-6 * time.Minute), Version: balances.Version, Deltas: ds}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.RefreshBalances(t.Context(), "bsc", f.now, balances.Version); err != nil {
		t.Fatal(err)
	}
	queueHolderMembers(t, f, "chain,address,label,tags,twitter\n"+
		"bsc,0x0000000000000000000000000000000000000011,M1,,,\n"+
		"bsc,0x0000000000000000000000000000000000000012,M2,,,\n"+
		"base,0x0000000000000000000000000000000000000013,Other chain,,,\n"+
		"bsc,0x0000000000000000000000000000000000000014,Excluded,,,\n"+
		"bsc,0x0000000000000000000000000000000000000015,Negative,,,\n")
	buildHolderMembers(t, f)
	if _, err := holderBackend(f).Exclude(t.Context(), api.BSC, "0x0000000000000000000000000000000000000014", "fixture exclusion"); err != nil {
		t.Fatal(err)
	}
	buildHolderMembers(t, f)
	return f
}

func TestPgTokenSmartMoneyAndCreator(t *testing.T) {
	f := smartMoneyHoldingsFixture(t)
	h := f.handler()
	var got api.TokenSmartMoney
	getPG(t, h, "/api/v1/tokens/{chain}/{address}/smart-money", holdingsToken+"/smart-money", 200, &got)
	if got.ListVersion != "3" || got.Holders == nil || *got.Holders != 1 {
		t.Errorf("active smart holders=%+v; want version 3, holders 1", got)
	}
	if len(got.Items) != 1 {
		t.Fatalf("cards=%+v; want only m1", got.Items)
	}
	row := got.Items[0]
	if row.Wallet.Address != "0x0000000000000000000000000000000000000011" || !reflect.DeepEqual(row.Wallet.Labels, []string{"smart_money"}) || row.Wallet.DisplayName == nil || *row.Wallet.DisplayName != "M1" {
		t.Errorf("wallet=%+v", row.Wallet)
	}
	if row.Balance == nil || *row.Balance != "100000000" || row.SharePct == nil || *row.SharePct != "10" {
		t.Errorf("balance/share=%+v;want 1e8 and 10%%", row)
	}
	// Only six minutes are covered: 24h totals remain unknown after H4.
	if row.BuyVolumeUSD != nil || row.SellVolumeUSD != nil || row.LastTradeAt != nil {
		t.Errorf("incomplete 24h volumes / no trade history=%+v", row)
	}
	for _, suffix := range []string{"", "?currency=native"} {
		var detail api.TokenDetail
		getPG(t, h, "/api/v1/tokens/{chain}/{address}", holdingsToken+suffix, 200, &detail)
		if detail.Holders == nil || *detail.Holders != 4 || detail.SMHolders == nil || *detail.SMHolders != 1 || detail.HolderCoverage.Status != "full" {
			t.Errorf("detail holders=%+v", detail)
		}
		if detail.Creator == nil || detail.Creator.HoldingPct == nil || *detail.Creator.HoldingPct != "2" {
			t.Errorf("creator=%+v;want 2%%", detail.Creator)
		}
	}
}
