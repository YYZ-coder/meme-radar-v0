package pgapi_test

import (
	"context"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

// Five formal parse facts across three blocks: descending (block,tx,ix)
// is (3,7,1),(3,6,2),(2,5,2),(2,5,1),(1,1,1), hence pages 2/2/1.
func TestPgLiquidityEventsPaginate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token, pool, quote, wallet := raw(20, 0x31), raw(20, 0xa3), raw(20, 0xc3), raw(20, 0xd3)
	nine := int16(9)
	positions := [][][2]int{{{1, 1}}, {{5, 1}, {5, 2}}, {{6, 2}, {7, 1}}}
	for n, pos := range positions {
		facts := parse.Facts{}
		if n == 0 {
			facts.Tokens = []parse.Token{{Address: token, Created: true, Symbol: "LQ", Decimals: &nine}}
			facts.Pools = []parse.Pool{{ID: pool, Token: token, Quote: quote, QuoteSymbol: "USDC", Protocol: "pancake_v2", Dex: "pancake_v2", Created: true}}
		}
		for _, p := range pos {
			facts.Liquidity = append(facts.Liquidity, parse.Liquidity{Tx: p[0], Ix: p[1], TxHash: raw(32, byte(p[0])), Pool: pool, Token: token, Wallet: wallet,
				Kind: "add", TokenAmount: "1234567890123456789", QuoteAmount: "1000000", QuoteSymbol: "USDC", Decoder: "test", Version: 1})
		}
		f.apply(t, "bsc", uint64(n+1), apiTime.Add(time.Duration(n-3)*time.Minute), facts)
	}
	if err := f.st.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "bsc", Asset: quote, Symbol: "USDC", Kind: "stable", Decimals: 6}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.RefreshCoverage(ctx, "bsc"); err != nil {
		t.Fatal(err)
	}
	const route = "/api/v1/tokens/{chain}/{address}/liquidity-events"
	const target = "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000031/liquidity-events?limit=2"
	h := f.handler()
	var ids []string
	cursor := ""
	for page, size := range []int{2, 2, 1} {
		var got api.LiquidityEvents
		getPG(t, h, route, target+"&cursor="+url.QueryEscape(cursor), 200, &got)
		if got.Coverage != "partial" || len(got.Items) != size {
			t.Fatalf("page %d = %+v, want partial and %d items", page, got, size)
		}
		for _, item := range got.Items {
			ids = append(ids, item.ID)
			if item.Kind != "add" || item.TokenAmount != "1234567890.123456789" || item.QuoteAmount == nil || *item.QuoteAmount != "1" || item.ValueUSD != nil || item.Wallet == nil || item.Wallet.Address != "0x00000000000000000000000000000000000000d3" || item.Wallet.Labels == nil || len(item.Wallet.Labels) != 0 || item.Pool != "0x00000000000000000000000000000000000000a3" || item.Dex != "pancake_v2" || item.QuoteSymbol == nil || *item.QuoteSymbol != "USDC" {
				t.Errorf("liquidity mapping = %+v", item)
			}
		}
		if page < 2 {
			if got.NextCursor == nil {
				t.Fatal("missing next cursor")
			}
			cursor = *got.NextCursor
		} else if got.NextCursor != nil {
			t.Errorf("last cursor = %q, want null", *got.NextCursor)
		}
	}
	if want := []string{"3:7:1", "3:6:2", "2:5:2", "2:5:1", "1:1:1"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("positions = %v, want %v", ids, want)
	}
	var failure api.ErrorBody
	getPG(t, h, route, target+"&cursor="+url.QueryEscape(cursor+"!"), 400, &failure)
	if failure.Error.Code != "invalid_param" || failure.Error.Field == nil || *failure.Error.Field != "cursor" {
		t.Errorf("bad cursor = %+v", failure)
	}
	getPG(t, h, route, "/api/v1/tokens/bsc/0x00000000000000000000000000000000000000ff/liquidity-events", 404, &failure)
}
