package pgapi_test

import (
	"testing"

	"github.com/meme-radar/radar/internal/api"
)

func TestPgTokenDetailNative(t *testing.T) {
	f := detailFixture(t, false)
	var got api.TokenDetail
	getPG(t, f.handler(), "/api/v1/tokens/{chain}/{address}", "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061?currency=native", 200, &got)
	if got.Currency != "BNB" || got.Price == nil || *got.Price != "0.5" || got.MarketCap == nil || *got.MarketCap != "500000000" || got.Liquidity == nil || *got.Liquidity != "4" || got.Volume24h == nil || *got.Volume24h != "4" || got.ATHMarketCap != nil || got.ATHSince == nil || *got.ATHSince != "2026-10-23T09:54:00.000Z" {
		t.Errorf("native header=%+v", got)
	}
	if len(got.Windows) != 4 {
		t.Fatalf("windows=%+v", got.Windows)
	}
	w := got.Windows[0]
	if w.Volume == nil || *w.Volume != "3.5" || w.NetInflow == nil || *w.NetInflow != "2.5" || w.BuyVolume == nil || *w.BuyVolume != "3" || w.SellVolume == nil || *w.SellVolume != "0.5" {
		t.Errorf("native 5m=%+v", w)
	}
}
