package pgapi_test

import (
	"testing"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
)

func TestPgMissingDecimalsReturns503(t *testing.T) {
	f := newFixture(t)
	token, pool, quote := raw(20, 0x51), raw(20, 0xa5), raw(20, 0xc5)
	f.apply(t, "bsc", 1, apiTime, parse.Facts{
		Tokens:    []parse.Token{{Address: token, Created: true, Symbol: "NO_DEC", TotalSupply: "1000000000000"}},
		Pools:     []parse.Pool{{ID: pool, Token: token, Quote: quote, Protocol: "pancake_v2", Dex: "pancake_v2", Created: true}},
		Trades:    []parse.Trade{{Tx: 1, Ix: 1, TxHash: raw(32, 1), Pool: pool, Token: token, Attribution: "ambiguous", Side: "buy", TokenAmount: "1000", QuoteAmount: "1", Decoder: "test", Version: 1}},
		Liquidity: []parse.Liquidity{{Tx: 2, Ix: 1, TxHash: raw(32, 2), Pool: pool, Token: token, Kind: "add", TokenAmount: "1000", QuoteAmount: "1", Decoder: "test", Version: 1}},
	})
	h := f.handler()
	const target = "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000051"
	const route = "/api/v1/tokens/{chain}/{address}"
	for _, suffix := range []string{"", "/activities", "/liquidity-events", "/holders", "/smart-money", "/candles?interval=1m&metric=market_cap"} {
		t.Run(suffix, func(t *testing.T) {
			path := suffix
			if suffix == "/candles?interval=1m&metric=market_cap" {
				path = "/candles"
			}
			var body api.ErrorBody
			getPG(t, h, route+path, target+suffix, 503, &body)
			if body.Error.Code != "api_unavailable" || body.Error.Message != "token decimals unknown" {
				t.Errorf("missing precision=%+v", body)
			}
		})
	}
	t.Run("price candles remain readable", func(t *testing.T) {
		var got api.Candles
		getPG(t, h, route+"/candles", target+"/candles?interval=1m&metric=price", 200, &got)
	})
	t.Run("pools remain readable", func(t *testing.T) {
		var got api.Pools
		getPG(t, h, route+"/pools", target+"/pools", 200, &got)
		if len(got.Items) != 1 || got.Items[0].ReserveToken != nil {
			t.Errorf("pool=%+v", got)
		}
	})
}
