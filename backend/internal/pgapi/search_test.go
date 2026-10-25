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

// Market expectations are hand-computed: 1e15 atomic / 1e6 = 1e9 whole tokens;
// USDC price per token .003/.005 gives market caps 3e6/5e6 respectively.
func TestPgSearch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	at := apiTime.Add(-time.Hour)
	a, b, lower, doge := raw(32, 0), raw(20, 0xab), raw(20, 0xbc), raw(32, 1)
	pA, pB, quote := raw(32, 11), raw(20, 12), raw(20, 9)
	six := int16(6)
	token := func(address []byte, symbol, name, platform string) parse.Token {
		return parse.Token{Address: address, Created: true, Symbol: symbol, Name: name, LaunchPlatform: platform, Decimals: &six, TotalSupply: "1000000000000000"}
	}
	pool := func(id, token []byte) parse.Pool {
		return parse.Pool{ID: id, Token: token, Quote: quote, QuoteSymbol: "USDC", Protocol: "pancake_v2", Dex: "test-dex", Created: true}
	}
	trade := func(id, token, hash []byte, amount, price string) parse.Trade {
		return parse.Trade{Tx: 1, Ix: 1, TxHash: hash, Pool: id, Token: token, Wallet: raw(len(token), 5), Attribution: "confirmed", Side: "buy", TokenAmount: "1000000", QuoteAmount: amount, QuoteSymbol: "USDC", PriceNative: price, Decoder: "test", Version: 1}
	}
	f.apply(t, "solana", 1, at, parse.Facts{Tokens: []parse.Token{token(a, "PEPE", "Pepe coin", "pump_fun"), token(doge, "DOGE", "Canine coin", "pump_fun")}, Pools: []parse.Pool{pool(pA, a)}, Trades: []parse.Trade{trade(pA, a, raw(64, 1), "3000", "0.003")}})
	f.apply(t, "bsc", 1, at, parse.Facts{Tokens: []parse.Token{token(b, "PEPE2", "Pepe two", "four_meme")}, Pools: []parse.Pool{pool(pB, b)}, Trades: []parse.Trade{trade(pB, b, raw(32, 1), "5000", "0.005")}})
	f.apply(t, "base", 1, at, parse.Facts{Tokens: []parse.Token{token(lower, "pepe-lower", "Pepe without market", "clanker")}})
	if err := f.st.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: at, USD: "100", Source: "coinbase"}, {Symbol: "BNB", Minute: at, USD: "200", Source: "coinbase"}}); err != nil {
		t.Fatal(err)
	}
	for _, chain := range []struct {
		name, native string
		token, pool  []byte
	}{{"solana", "SOL", a, pA}, {"bsc", "BNB", b, pB}} {
		if err := f.st.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: chain.name, Asset: quote, Symbol: "USDC", Kind: "stable", Decimals: 6}}); err != nil {
			t.Fatal(err)
		}
		if err := f.st.FillValues(ctx, chain.name, chain.native, at); err != nil {
			t.Fatal(err)
		}
		if err := f.st.SaveMainPools(ctx, chain.name, []store.MainPoolChange{{Token: chain.token, Pool: chain.pool, Reason: "liquidity"}}, at); err != nil {
			t.Fatal(err)
		}
		if err := f.st.UpdateMarket(ctx, chain.name, f.now, false); err != nil {
			t.Fatal(err)
		}
	}
	h := f.handler()
	const route = "/api/v1/search"
	var got api.Search
	getPG(t, h, route, route+"?q=pep", 200, &got)
	symbols := func(s api.Search) []string {
		out := []string{}
		for _, v := range s.Tokens {
			out = append(out, v.Symbol)
		}
		return out
	}
	if want := []string{"PEPE2", "PEPE", "pepe-lower"}; !reflect.DeepEqual(symbols(got), want) {
		t.Fatalf("symbols=%v, want %v", symbols(got), want)
	}
	if got.Tokens[0].MarketCapUSD == nil || *got.Tokens[0].MarketCapUSD != "5000000" || got.Tokens[1].MarketCapUSD == nil || *got.Tokens[1].MarketCapUSD != "3000000" || got.Tokens[2].MarketCapUSD != nil {
		t.Errorf("market caps must be 5000000/3000000/NULL: %+v", got.Tokens)
	}
	pepe := got.Tokens[1]
	if pepe.Address != "11111111111111111111111111111111" || pepe.ImageURL != nil || pepe.Name != "Pepe coin" || pepe.LaunchPlatform == nil || *pepe.LaunchPlatform != "pump_fun" || pepe.LaunchAt == nil || *pepe.LaunchAt != "2026-10-23T09:00:00.000Z" || pepe.AgeSeconds == nil || *pepe.AgeSeconds != 3630 || pepe.LastTradeAt == nil || *pepe.LastTradeAt != "2026-10-23T09:00:00.000Z" {
		t.Errorf("PEPE ref/time mapping=%+v", pepe)
	}
	if got.Wallets == nil || len(got.Wallets) != 0 {
		t.Errorf("wallets=%+v, want empty array before H3", got.Wallets)
	}
	for _, tt := range []struct {
		query string
		want  []string
	}{
		{"q=11111111111111111111111111111111", []string{"PEPE"}},
		{"q=0x00000000000000000000000000000000000000AB", []string{"PEPE2"}},
		{"q=pep&chain=bsc", []string{"PEPE2"}},
		{"q=pep&limit=1", []string{"PEPE2"}},
		{"q=PEP", []string{"PEPE2", "PEPE", "pepe-lower"}},
		{"q=CAN", []string{"DOGE"}},
		{"q=not-present", []string{}},
		{"q=" + url.QueryEscape("pe%"), []string{}},
		{"q=" + url.QueryEscape("pe_"), []string{}},
		{"q=0x00000000000000000000000000000000000000ab&chain=solana", []string{}},
	} {
		t.Run(tt.query, func(t *testing.T) {
			var page api.Search
			getPG(t, h, route, route+"?"+tt.query, 200, &page)
			if !reflect.DeepEqual(symbols(page), tt.want) {
				t.Errorf("symbols=%v, want %v", symbols(page), tt.want)
			}
			if page.Tokens == nil || page.Wallets == nil {
				t.Error("empty collections must be arrays")
			}
		})
	}
	var indexes int
	if err := f.conn.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND tablename='tokens' AND (indexdef LIKE '%(chain, lower(symbol) text_pattern_ops)%' OR indexdef LIKE '%(lower(name) text_pattern_ops)%')`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 2 {
		t.Errorf("specified prefix indexes=%d, want 2", indexes)
	}
}
