package pgapi_test

import (
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

// Regression for the single-statement identity: old and new copies coexist,
// but only the active version of the requested chain/token contributes.
func TestPgTokenSmartMoneyVolumesMembershipAndScope(t *testing.T) {
	f := smartMoneyVolumeFixture(t)
	at := f.now.Add(-time.Minute)
	six := int16(6)
	trade := func(tx int, token, pool []byte, side, atomic, whole string) parse.Trade {
		return parse.Trade{Tx: tx, Ix: 1, TxHash: raw(32, byte(tx)), Token: token, Pool: pool, Wallet: raw(20, 0x11), Attribution: "confirmed", Side: side, TokenAmount: "1000000", QuoteAmount: atomic, QuoteSymbol: "BNB", PriceNative: whole, ValueNative: whole, Decoder: "test", Version: 1}
	}
	ambiguous := trade(3, raw(20, 0x61), raw(20, 0xa6), "buy", "999000000000000000000", "999")
	ambiguous.Attribution = "ambiguous"
	f.apply(t, "bsc", 4, at, parse.Facts{
		Tokens: []parse.Token{{Address: raw(20, 0x62), Created: true, Decimals: &six}},
		Pools:  []parse.Pool{{ID: raw(20, 0xa7), Token: raw(20, 0x62), Quote: raw(20, 0xc6), QuoteSymbol: "BNB", Protocol: "test", Dex: "test", Created: true}},
		Trades: []parse.Trade{trade(1, raw(20, 0x61), raw(20, 0xa6), "sell", "4250000000000000000", "4.25"), trade(2, raw(20, 0x62), raw(20, 0xa7), "buy", "999000000000000000000", "999"), ambiguous},
	})
	other := trade(1, raw(20, 0x61), raw(20, 0xa6), "buy", "888000000000000000000", "888")
	other.QuoteSymbol = "ETH"
	f.apply(t, "base", 1, at, parse.Facts{Tokens: []parse.Token{{Address: raw(20, 0x61), Decimals: &six}}, Pools: []parse.Pool{{ID: raw(20, 0xa6), Token: raw(20, 0x61), Quote: raw(20, 0xc6), QuoteSymbol: "ETH", Protocol: "test", Dex: "test", Created: true}}, Trades: []parse.Trade{other}})
	for _, err := range []error{
		f.st.SaveQuoteAssets(t.Context(), []store.QuoteAsset{{Chain: "base", Asset: raw(20, 0xc6), Symbol: "ETH", Kind: "native", Decimals: 18}}),
		f.st.SaveQuotePrices(t.Context(), []store.QuotePrice{{Symbol: "ETH", Minute: at.Truncate(time.Minute), USD: "1", Source: "binance"}}),
		f.st.FillValues(t.Context(), "bsc", "BNB", at), f.st.FillValues(t.Context(), "base", "ETH", at),
		f.st.RefreshSmTrades(t.Context(), "bsc", f.now),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	queueHolderMembers(t, f, "chain,address,label\nbsc,0x0000000000000000000000000000000000000011,M1\nbsc,0x0000000000000000000000000000000000000012,M2\nbase,0x0000000000000000000000000000000000000011,Other chain\n")
	check := func(version string) {
		t.Helper()
		var got api.TokenSmartMoney
		getPG(t, f.handler(), "/api/v1/tokens/{chain}/{address}/smart-money", holdingsToken+"/smart-money", 200, &got)
		if got.ListVersion != version || len(got.Items) != 1 {
			t.Fatalf("active cards=%+v; want v%s, M1 only", got, version)
		}
		row := got.Items[0]
		if row.BuyVolumeUSD == nil || *row.BuyVolumeUSD != "14.345678901234567891" || row.SellVolumeUSD == nil || *row.SellVolumeUSD != "4.25" {
			t.Errorf("scoped amounts=%+v; must ignore other version/token/chain and ambiguous attribution", row)
		}
	}
	check("2") // A pending management intent cannot change public amounts.
	buildHolderMembers(t, f)
	check("3") // Old version 2 copies still exist, but cannot double the total.
}
