package pgapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestPgActivitiesUsesProjectedSupply(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token, pool, quote := raw(20, 0x42), raw(20, 0xa4), raw(20, 0xc4)
	six := int16(6)
	at := apiTime.Add(-time.Minute)
	f.apply(t, "bsc", 1, at, parse.Facts{Tokens: []parse.Token{{Address: token, Created: true, Decimals: &six, TotalSupply: "1000000000000000"}}, Pools: []parse.Pool{{ID: pool, Token: token, Quote: quote, QuoteSymbol: "USDC", Protocol: "pancake_v2", Dex: "pancake_v2", Created: true}}, Trades: []parse.Trade{{Tx: 1, Ix: 1, TxHash: raw(32, 1), Pool: pool, Token: token, Attribution: "confirmed", Side: "buy", TokenAmount: "1000000", QuoteAmount: "1000", QuoteSymbol: "USDC", PriceNative: "0.001", Decoder: "test", Version: 1}}})
	for _, step := range []func() error{
		func() error {
			return f.st.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "bsc", Asset: quote, Symbol: "USDC", Kind: "stable", Decimals: 6}})
		},
		func() error {
			return f.st.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "BNB", Minute: at, USD: "200", Source: "coinbase"}})
		},
		func() error { return f.st.FillValues(ctx, "bsc", "BNB", at) },
		func() error {
			return f.st.SaveMainPools(ctx, "bsc", []store.MainPoolChange{{Token: token, Pool: pool, Reason: "liquidity"}}, at)
		},
		func() error { return f.st.UpdateMarket(ctx, "bsc", f.now, false) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	// Parse updates authoritative supply before compute's next pass. The API
	// must use the published circulating_supply (1e9), not the new raw total.
	f.apply(t, "bsc", 2, apiTime, parse.Facts{Tokens: []parse.Token{{Address: token, TotalSupply: "2000000000000000"}}})
	var got api.Activities
	getPG(t, f.handler(), "/api/v1/tokens/{chain}/{address}/activities", "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000042/activities", 200, &got)
	if len(got.Items) != 1 || got.Items[0].MarketCapUSD == nil || *got.Items[0].MarketCapUSD != "1000000" {
		t.Errorf("projected market cap must remain .001 x 1e9 = 1000000: %+v", got.Items)
	}
}
