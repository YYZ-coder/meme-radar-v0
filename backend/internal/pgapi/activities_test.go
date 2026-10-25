package pgapi_test

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

// One billion whole tokens x 0.001 USD gives 1,000,000 USD market cap.
// At the same block time (3,8,1) precedes (3,7,1); liquidity has no trade fields.
func TestPgActivities(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token, pool, quote, wallet := raw(20, 0x41), raw(20, 0xa4), raw(20, 0xc4), raw(20, 0xd4)
	six := int16(6)
	trade := func(tx int, side string) parse.Trade {
		return parse.Trade{Tx: tx, Ix: 1, TxHash: raw(32, byte(tx)), Pool: pool, Token: token, Wallet: wallet, Attribution: "confirmed", Side: side, TokenAmount: "1000000", QuoteAmount: "1000", QuoteSymbol: "USDC", PriceNative: "0.001", Decoder: "test", Version: 1}
	}
	liq := func(tx int, kind string) parse.Liquidity {
		return parse.Liquidity{Tx: tx, Ix: 1, TxHash: raw(32, byte(tx)), Pool: pool, Token: token, Kind: kind, TokenAmount: "2000000", QuoteAmount: "0", QuoteSymbol: "USDC", Decoder: "test", Version: 1}
	}
	f.apply(t, "bsc", 1, apiTime.Add(-3*time.Minute), parse.Facts{Tokens: []parse.Token{{Address: token, Created: true, Symbol: "ACT", Decimals: &six, TotalSupply: "1000000000000000"}}, Pools: []parse.Pool{{ID: pool, Token: token, Quote: quote, QuoteSymbol: "USDC", Protocol: "pancake_v2", Dex: "pancake_v2", Created: true}}, Trades: []parse.Trade{trade(2, "buy")}})
	f.apply(t, "bsc", 2, apiTime.Add(-2*time.Minute), parse.Facts{Liquidity: []parse.Liquidity{liq(5, "add")}})
	f.apply(t, "bsc", 3, apiTime.Add(-time.Minute), parse.Facts{Trades: []parse.Trade{trade(8, "sell")}, Liquidity: []parse.Liquidity{liq(7, "remove")}})
	if err := f.st.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "bsc", Asset: quote, Symbol: "USDC", Kind: "stable", Decimals: 6}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "BNB", Minute: apiTime.Add(-3 * time.Minute), USD: "200", Source: "coinbase"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.FillValues(ctx, "bsc", "BNB", apiTime.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SaveMainPools(ctx, "bsc", []store.MainPoolChange{{Token: token, Pool: pool, Reason: "liquidity"}}, apiTime.Add(-3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpdateMarket(ctx, "bsc", f.now, false); err != nil {
		t.Fatal(err)
	}
	if err := f.st.RefreshCoverage(ctx, "bsc"); err != nil {
		t.Fatal(err)
	}
	h := f.handler()
	const route = "/api/v1/tokens/{chain}/{address}/activities"
	const target = "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000041/activities"
	var got api.Activities
	getPG(t, h, route, target+"?limit=2", 200, &got)
	if len(got.Items) != 2 || got.NextCursor == nil {
		t.Fatalf("first page=%+v", got)
	}
	first := got.Items[0]
	if first.ID != "3:8:1" || first.Kind != "sell" || first.Attribution == nil || *first.Attribution != "confirmed" || first.TokenAmount != "1" || first.QuoteAmount == nil || *first.QuoteAmount != "0.001" || first.ValueUSD == nil || *first.ValueUSD != "0.001" || first.PriceUSD == nil || *first.PriceUSD != "0.001" || first.MarketCapUSD == nil || *first.MarketCapUSD != "1000000" || first.Wallet == nil || first.Wallet.Address != "0x00000000000000000000000000000000000000d4" {
		t.Errorf("trade=%+v", first)
	}
	second := got.Items[1]
	if second.ID != "3:7:1" || second.Kind != "remove" || second.Attribution != nil || second.PriceUSD != nil || second.MarketCapUSD != nil || (second.QuoteAmount == nil || *second.QuoteAmount != "0") || second.Wallet != nil {
		t.Errorf("liquidity=%+v", second)
	}
	if got.Coverage != "partial" || got.LabelCountsCoverage != "partial" || got.LabelCounts == nil || len(got.LabelCounts) != 2 || got.LabelCounts["all"] != nil || got.LabelCounts["smart_money"] != nil {
		t.Errorf("coverage/counts=%+v", got)
	}
	var next api.Activities
	getPG(t, h, route, target+"?limit=2&cursor="+url.QueryEscape(*got.NextCursor), 200, &next)
	if len(next.Items) != 2 || next.Items[0].ID != "2:5:1" || next.Items[1].ID != "1:2:1" || next.NextCursor != nil {
		t.Errorf("second page=%+v", next)
	}
	for _, tc := range []struct {
		query string
		ids   []string
	}{
		{"?kind=trade", []string{"3:8:1", "1:2:1"}},
		{"?kind=liquidity", []string{"3:7:1", "2:5:1"}},
		{fmt.Sprintf("?from=%d&to=%d", apiTime.Add(-2*time.Minute).Unix(), apiTime.Add(-time.Minute).Unix()), []string{"2:5:1"}},
		{"?label=smart_money", []string{}},
	} {
		var page api.Activities
		getPG(t, h, route, target+tc.query, 200, &page)
		ids := []string{}
		for _, a := range page.Items {
			ids = append(ids, a.ID)
		}
		if !reflect.DeepEqual(ids, tc.ids) || page.Items == nil {
			t.Errorf("%s IDs=%v, want %v", tc.query, ids, tc.ids)
		}
	}
	var failure api.ErrorBody
	getPG(t, h, route, target+"?cursor=invalid!", 400, &failure)
}
