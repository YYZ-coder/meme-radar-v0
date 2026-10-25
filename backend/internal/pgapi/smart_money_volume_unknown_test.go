package pgapi_test

import (
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
)

// A missing side is a known zero; a side containing any unpriced trade is
// unknown even when other trades on that side have prices. Opposite sides
// remain independent. These are literal applications of 00-common section 4.
func TestPgTokenSmartMoneyVolumesUnknownAndZero(t *testing.T) {
	for _, tc := range []struct{ name, side, buy, sell string }{
		{"no buys", "", "0", "4.25"},
		{"unpriced buy plus priced buy", "buy", "NULL", "4.25"},
		{"unpriced sell plus priced sell", "sell", "14.345678901234567891", "NULL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := smartMoneyVolumeFixture(t)
			if tc.side == "" {
				f.apply(t, "bsc", 2, f.now.Add(-24*time.Hour), parse.Facts{})
				f.apply(t, "bsc", 3, f.now.Add(-23*time.Hour), parse.Facts{})
			} else {
				at, num := f.now.Add(-24*time.Hour), uint64(2)
				if tc.side == "sell" {
					at, num = f.now.Add(-time.Minute), 4
				}
				trades := []parse.Trade{{Tx: 1, Ix: 1, TxHash: raw(32, 1), Pool: raw(20, 0xa7), Token: raw(20, 0x61), Wallet: raw(20, 0x11), Attribution: "confirmed", Side: tc.side, TokenAmount: "1000000", QuoteAmount: "1000000", Decoder: "test", Version: 1}}
				if tc.side == "sell" {
					trades = append(trades, parse.Trade{Tx: 2, Ix: 1, TxHash: raw(32, 2), Pool: raw(20, 0xa6), Token: raw(20, 0x61), Wallet: raw(20, 0x11), Attribution: "confirmed", Side: "sell", TokenAmount: "1000000", QuoteAmount: "4250000000000000000", QuoteSymbol: "BNB", ValueNative: "4.25", PriceNative: "4.25", Decoder: "test", Version: 1})
				}
				f.apply(t, "bsc", num, at, parse.Facts{Pools: []parse.Pool{{ID: raw(20, 0xa7), Token: raw(20, 0x61), Quote: raw(20, 0xc7), Protocol: "test", Dex: "unknown", Created: true}}, Trades: trades})
				if err := f.st.FillValues(t.Context(), "bsc", "BNB", at); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.st.RefreshSmTrades(t.Context(), "bsc", f.now); err != nil {
				t.Fatal(err)
			}
			var got api.TokenSmartMoney
			getPG(t, f.handler(), "/api/v1/tokens/{chain}/{address}/smart-money", holdingsToken+"/smart-money", 200, &got)
			if len(got.Items) != 1 {
				t.Fatalf("cards=%+v", got)
			}
			for _, v := range []struct {
				name   string
				amount *string
				want   string
			}{
				{"buy", got.Items[0].BuyVolumeUSD, tc.buy}, {"sell", got.Items[0].SellVolumeUSD, tc.sell},
			} {
				actual := "NULL"
				if v.amount != nil {
					actual = *v.amount
				}
				if actual != v.want {
					t.Errorf("%s amount=%s; want %s", v.name, actual, v.want)
				}
			}
		})
	}
}
