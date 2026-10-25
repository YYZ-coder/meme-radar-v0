package pgapi_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestPgPools(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token, empty, quote := raw(20, 0x2c), raw(20, 0x2d), raw(20, 0xc1)
	a, b := raw(20, 0xa1), raw(32, 0xb2)
	nine := int16(9)
	f.apply(t, "bsc", 1, apiTime.Add(-5*time.Minute), parse.Facts{
		Tokens: []parse.Token{{Address: token, Created: true, Symbol: "POOL", Name: "Pool token", Decimals: &nine, TotalSupply: "100000000000000000000"}, {Address: empty, Created: true, Symbol: "EMPTY", Name: "No pools"}},
		Pools: []parse.Pool{
			{ID: a, Token: token, Quote: quote, QuoteSymbol: "USDC", Protocol: "pancake_v2", Dex: "pancake_v2", Created: true, Status: "active", ReserveToken: "1234567890123456789", ReserveQuote: "10000000000", Tx: 1, Ix: 1},
			{ID: b, Token: token, Quote: quote, QuoteSymbol: "USDC", Protocol: "uniswap_v4", Dex: "uniswap_v4", Created: true, Status: "graduated", Tx: 2, Ix: 1},
		},
	})
	if err := f.st.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "bsc", Asset: quote, Symbol: "USDC", Kind: "stable", Decimals: 6}}); err != nil {
		t.Fatal(err)
	}
	// Formal compute paths price A as 10000 whole USDC x 2 = 20000 USD.
	// V4 B has no reserve sample and therefore no pool_market row.
	if err := f.st.SampleLiquidity(ctx, "bsc"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.PriceLiquidity(ctx, "bsc", "BNB", f.now); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SaveMainPools(ctx, "bsc", []store.MainPoolChange{{Token: token, Pool: a, Reason: "liquidity"}}, apiTime); err != nil {
		t.Fatal(err)
	}
	h := f.handler()
	const route = "/api/v1/tokens/{chain}/{address}/pools"
	var got api.Pools
	getPG(t, h, route, "/api/v1/tokens/bsc/0x000000000000000000000000000000000000002C/pools", 200, &got)
	if len(got.Items) != 2 {
		t.Fatalf("pools=%+v, want two", got.Items)
	}
	byAddress := map[string]api.Pool{}
	for _, p := range got.Items {
		byAddress[p.Address] = p
	}
	pa, ok := byAddress["0x00000000000000000000000000000000000000a1"]
	if !ok {
		t.Fatalf("pool A absent: %+v", got.Items)
	}
	if pa.Protocol != "pancake_v2" || pa.Dex != "pancake_v2" || pa.QuoteSymbol == nil || *pa.QuoteSymbol != "USDC" || pa.Status != "active" || !pa.IsMain || pa.LiquidityUSD == nil || *pa.LiquidityUSD != "20000" || pa.ReserveToken == nil || *pa.ReserveToken != "1234567890.123456789" || pa.ReserveQuote == nil || *pa.ReserveQuote != "10000" || pa.CreatedAt == nil || *pa.CreatedAt != "2026-10-23T09:55:00.000Z" {
		t.Errorf("pool A mapping=%+v", pa)
	}
	pb, ok := byAddress["0x00000000000000000000000000000000000000000000000000000000000000b2"]
	if !ok {
		t.Fatalf("32-byte V4 pool absent: %+v", got.Items)
	}
	if pb.Protocol != "uniswap_v4" || pb.Dex != "uniswap_v4" || pb.Status != "graduated" || pb.IsMain || pb.LiquidityUSD != nil || pb.ReserveToken != nil || pb.ReserveQuote != nil || !reflect.DeepEqual(pb.QuoteSymbol, pa.QuoteSymbol) || !reflect.DeepEqual(pb.CreatedAt, pa.CreatedAt) {
		t.Errorf("pool B mapping=%+v, want unknown liquidity/reserves and original metadata", pb)
	}
	var noPools api.Pools
	getPG(t, h, route, "/api/v1/tokens/bsc/0x000000000000000000000000000000000000002d/pools", 200, &noPools)
	if noPools.Items == nil || len(noPools.Items) != 0 {
		t.Errorf("empty token pools=%+v, want []", noPools.Items)
	}
	var failure api.ErrorBody
	getPG(t, h, route, "/api/v1/tokens/bsc/0x00000000000000000000000000000000000000ff/pools", 404, &failure)
	if failure.Error.Code != "not_found" {
		t.Errorf("missing token code=%s", failure.Error.Code)
	}
	getPG(t, h, route, "/api/v1/tokens/solana/"+strings.Repeat("1", 33)+"/pools", 400, &failure)
	if failure.Error.Code != "invalid_param" || failure.Error.Field == nil || *failure.Error.Field != "address" {
		t.Errorf("invalid Solana key response=%+v", failure)
	}
}
