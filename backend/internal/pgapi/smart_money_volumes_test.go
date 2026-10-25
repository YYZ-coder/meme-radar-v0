package pgapi_test

import (
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/balances"
	"github.com/meme-radar/radar/internal/store"
)

// Prices and membership use their production writers. BNB is independently
// fixed at $1, so literal native amounts are also the expected USD amounts.
func smartMoneyVolumeFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	token, pool, quote := raw(20, 0x61), raw(20, 0xa6), raw(20, 0xc6)
	six := int16(6)
	created := f.now.Add(-25 * time.Hour)
	f.apply(t, "bsc", 1, created, parse.Facts{
		Tokens: []parse.Token{{Address: token, Created: true, Decimals: &six, TotalSupply: "1000000000000000"}},
		Pools:  []parse.Pool{{ID: pool, Token: token, Quote: quote, QuoteSymbol: "BNB", Protocol: "pancake_v2", Dex: "pancake_v2", Created: true}},
	})
	prices := []store.QuotePrice{}
	for i, v := range []struct {
		at                  time.Time
		side, atomic, whole string
	}{
		{f.now.Add(-24 * time.Hour), "buy", "12345678901234567891", "12.345678901234567891"},
		{f.now.Add(-23 * time.Hour), "buy", "2000000000000000000", "2"},
		{f.now.Add(-time.Minute), "sell", "4250000000000000000", "4.25"},
		{f.now, "buy", "900000000000000000000", "900"},
	} {
		f.apply(t, "bsc", uint64(i+2), v.at, parse.Facts{Trades: []parse.Trade{{
			Tx: 1, Ix: 1, TxHash: raw(32, byte(i+2)), Token: token, Pool: pool, Wallet: raw(20, 0x11),
			Attribution: "confirmed", Side: v.side, TokenAmount: "1000000", QuoteAmount: v.atomic,
			QuoteSymbol: "BNB", PriceNative: v.whole, ValueNative: v.whole, Decoder: "test", Version: 1,
		}}})
		prices = append(prices, store.QuotePrice{Symbol: "BNB", Minute: v.at.Truncate(time.Minute), USD: "1", Source: "binance"})
	}
	f.apply(t, "bsc", 6, f.now.Add(time.Second), parse.Facts{})
	for _, err := range []error{
		f.st.SaveQuoteAssets(t.Context(), []store.QuoteAsset{{Chain: "bsc", Asset: quote, Symbol: "BNB", Kind: "native", Decimals: 18}}),
		f.st.SaveQuotePrices(t.Context(), prices),
		f.st.FillValues(t.Context(), "bsc", "BNB", created),
		f.st.RefreshCoverage(t.Context(), "bsc"),
		f.st.ApplyBalances(t.Context(), []store.BalanceBlock{{Chain: "bsc", Num: 1, Time: created, Version: balances.Version,
			Deltas: []parse.Balance{{Token: token, Owner: raw(20, 0x11), Delta: "100000000"}}}}),
		f.st.RefreshBalances(t.Context(), "bsc", f.now, balances.Version),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	queueHolderMembers(t, f, "chain,address,label\nbsc,0x0000000000000000000000000000000000000011,M1\n")
	buildHolderMembers(t, f)
	if err := f.st.RefreshSmTrades(t.Context(), "bsc", f.now); err != nil {
		t.Fatal(err)
	}
	return f
}

// H5-8's public HTTP seam. Hand sum: 12.345678901234567891 + 2 =
// 14.345678901234567891 bought; 4.25 sold. The 24h start is included,
// exactly-now is excluded, and all 18 fractional digits must survive.
func TestPgTokenSmartMoneyVolumes24h(t *testing.T) {
	f := smartMoneyVolumeFixture(t)
	var got api.TokenSmartMoney
	getPG(t, f.handler(), "/api/v1/tokens/{chain}/{address}/smart-money", holdingsToken+"/smart-money", 200, &got)
	if got.ListVersion != "2" || len(got.Items) != 1 {
		t.Fatalf("cards=%+v; want active v2 M1 only", got)
	}
	row := got.Items[0]
	if row.BuyVolumeUSD == nil || *row.BuyVolumeUSD != "14.345678901234567891" {
		t.Errorf("buy_volume_usd=%v; want 14.345678901234567891", row.BuyVolumeUSD)
	}
	if row.SellVolumeUSD == nil || *row.SellVolumeUSD != "4.25" {
		t.Errorf("sell_volume_usd=%v; want 4.25", row.SellVolumeUSD)
	}
}
