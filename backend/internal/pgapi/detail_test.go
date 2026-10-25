package pgapi_test

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

// This reuses H1's 5m worked example with API-valid EVM keys and BNB's 18
// decimals: buys 1+2 BNB, sell .5 BNB; at 100 USD/BNB => 350 volume, 250 net.
// A .5 BNB trade just before the window makes 24h volume 400, net 300.
// Each of two pools has 1 BNB quote reserve: 2 x 2 x 1 x 100 = 400 USD liquidity.
func detailFixture(t *testing.T, partial bool, noCoverage ...bool) *fixture {
	t.Helper()
	f := newFixture(t)
	ctx := context.Background()
	token, a, b, quote := raw(20, 0x61), raw(20, 0xa6), raw(20, 0xb6), raw(20, 0xc6)
	other1, other2, p1, p2 := raw(20, 0x62), raw(20, 0x63), raw(20, 0xa7), raw(20, 0xa8)
	six := int16(6)
	trade := func(pool, token []byte, amount, price, side string, w byte) parse.Trade {
		return parse.Trade{Tx: 1, Ix: 1, TxHash: raw(32, w), Pool: pool, Token: token, Wallet: raw(20, w), Attribution: "confirmed", Side: side, TokenAmount: "1000000", QuoteAmount: amount, QuoteSymbol: "BNB", PriceNative: price, ValueNative: price, Decoder: "test", Version: 1}
	}
	start := apiTime.Add(-5 * time.Minute)
	facts := map[time.Time]parse.Facts{
		start.Add(-time.Minute): {Tokens: []parse.Token{
			{Address: token, Created: true, Symbol: "DEMO", Name: "Demo token", LaunchPlatform: "four_meme", Creator: raw(20, 0xee), Decimals: &six, TotalSupply: "1000000000000000"},
			{Address: other1, Created: true, Symbol: "demo", Name: "Older", Decimals: &six, TotalSupply: "1000000000000000"},
			{Address: other2, Created: true, Symbol: "DEMO", Name: "Newer", Decimals: &six, TotalSupply: "1000000000000000"}},
			Pools: []parse.Pool{
				{ID: a, Token: token, Quote: quote, QuoteSymbol: "BNB", Protocol: "pancake_v2", Dex: "pancake_v2", Created: true, ReserveToken: "1000000", ReserveQuote: "1000000000000000000"},
				{ID: b, Token: token, Quote: quote, QuoteSymbol: "BNB", Protocol: "pancake_v2", Dex: "pancake_v2", Created: true, ReserveToken: "1000000", ReserveQuote: "1000000000000000000"},
				{ID: p1, Token: other1, Quote: quote, QuoteSymbol: "BNB", Protocol: "pancake_v2", Dex: "pancake_v2", Created: true},
				{ID: p2, Token: other2, Quote: quote, QuoteSymbol: "BNB", Protocol: "pancake_v2", Dex: "pancake_v2", Created: true}}},
		start.Add(-30 * time.Second):              {Trades: []parse.Trade{trade(a, token, "500000000000000000", "0.5", "buy", 1)}},
		start.Add(5 * time.Second):                {Trades: []parse.Trade{trade(a, token, "1000000000000000000", "1", "buy", 1)}},
		start.Add(15 * time.Second):               {Trades: []parse.Trade{trade(a, token, "500000000000000000", "0.5", "sell", 2)}},
		start.Add(3*time.Minute + 5*time.Second):  {Trades: []parse.Trade{trade(b, token, "2000000000000000000", "2", "buy", 3)}},
		start.Add(time.Minute + 20*time.Second):   {Trades: []parse.Trade{trade(p1, other1, "1000000000000000000", "1", "buy", 4)}},
		start.Add(4*time.Minute + 20*time.Second): {Trades: []parse.Trade{trade(p2, other2, "1000000000000000000", "1", "buy", 5)}},
	}
	times := map[time.Time]bool{}
	for at := start.Add(-time.Minute); !at.After(f.now); at = at.Add(10 * time.Second) {
		times[at] = true
	}
	for at := range facts {
		times[at] = true
	}
	ordered := []time.Time{}
	for at := range times {
		ordered = append(ordered, at)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })
	for n, at := range ordered {
		if partial && at.Equal(start.Add(2*time.Minute)) {
			continue
		}
		f.apply(t, "bsc", uint64(n+1), at, facts[at])
	}
	prices := []store.QuotePrice{}
	for n := -1; n <= 5; n++ {
		prices = append(prices, store.QuotePrice{Symbol: "BNB", Minute: start.Add(time.Duration(n) * time.Minute), USD: "100", Source: "coinbase"})
	}
	for _, step := range []func() error{
		func() error {
			return f.st.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "bsc", Asset: quote, Symbol: "BNB", Kind: "native", Decimals: 18}})
		},
		func() error { return f.st.SaveQuotePrices(ctx, prices) },
		func() error { return f.st.FillValues(ctx, "bsc", "BNB", start.Add(-time.Minute)) },
		func() error { return f.st.SampleLiquidity(ctx, "bsc") },
		func() error { return f.st.PriceLiquidity(ctx, "bsc", "BNB", f.now) },
		func() error {
			return f.st.SaveMainPools(ctx, "bsc", []store.MainPoolChange{{Token: token, Pool: a, Reason: "liquidity"}, {Token: other1, Pool: p1, Reason: "liquidity"}, {Token: other2, Pool: p2, Reason: "liquidity"}}, start.Add(-time.Minute))
		},
		func() error { return f.st.UpdateMarket(ctx, "bsc", f.now, false) },
		func() error { return f.st.RefreshStats(ctx, "bsc", f.now) },
		func() error {
			if len(noCoverage) > 0 && noCoverage[0] {
				return nil
			}
			return f.st.RefreshCoverage(ctx, "bsc")
		},
		func() error { return f.st.RefreshATH(ctx, "bsc") },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestPgTokenDetail(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete5m", true: "partial5m"}[partial], func(t *testing.T) {
			f := detailFixture(t, partial)
			var got api.TokenDetail
			getPG(t, f.handler(), "/api/v1/tokens/{chain}/{address}", "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061", 200, &got)
			if got.Token.Chain != api.BSC || got.Token.Address != "0x0000000000000000000000000000000000000061" || got.Token.Symbol != "DEMO" || got.Token.Name != "Demo token" || got.Token.ImageURL != nil || got.Token.LaunchAt == nil || *got.Token.LaunchAt != "2026-10-23T09:54:00.000Z" || got.Token.LaunchPlatform == nil || *got.Token.LaunchPlatform != "four_meme" {
				t.Errorf("token=%+v", got.Token)
			}
			if got.Currency != "USD" || got.AsOf != "2026-10-23T10:00:00.000Z" || got.Decimals != 6 || got.TotalSupply == nil || *got.TotalSupply != "1000000000" || got.Price == nil || *got.Price != "50" || got.MarketCap == nil || *got.MarketCap != "50000000000" || got.Liquidity == nil || *got.Liquidity != "400" || got.Volume24h == nil || *got.Volume24h != "400" || got.PriceChange24hPct == nil || *got.PriceChange24hPct != "0" {
				t.Errorf("amounts=%+v", got)
			}
			if got.Creator == nil || got.Creator.Address != "0x00000000000000000000000000000000000000ee" || got.Creator.HoldingPct != nil || got.MainPool == nil || got.MainPool.Address != "0x00000000000000000000000000000000000000a6" || got.MainPool.Dex != "pancake_v2" || got.MainPool.QuoteSymbol == nil || *got.MainPool.QuoteSymbol != "BNB" {
				t.Errorf("creator/pool=%+v / %+v", got.Creator, got.MainPool)
			}
			if got.Holders != nil || got.SMHolders != nil || got.HolderCoverage.Status != "unknown" || got.HolderCoverage.Since != nil || got.HolderCoverage.AsOf != got.AsOf || got.Socials != (api.Socials{}) || got.ATHMarketCap == nil || *got.ATHMarketCap != "100000000000" || got.ATHSince == nil || *got.ATHSince != "2026-10-23T09:54:00.000Z" {
				t.Errorf("unknown/ATH=%+v", got)
			}
			if len(got.Windows) != 4 {
				t.Fatalf("windows=%+v", got.Windows)
			}
			for i, w := range got.Windows {
				volume, net, buy := "400", "300", "350"
				buys := int64(3)
				if i == 0 {
					volume, net, buy = "350", "250", "300"
					buys = 2
				}
				if w.Window != []string{"5m", "1h", "4h", "24h"}[i] || w.Volume == nil || *w.Volume != volume || w.NetInflow == nil || *w.NetInflow != net || w.BuyVolume == nil || *w.BuyVolume != buy || w.SellVolume == nil || *w.SellVolume != "50" || w.Traders == nil || *w.Traders != 3 || w.Buys == nil || *w.Buys != buys || w.Sells == nil || *w.Sells != 1 || w.PriceChangePct == nil || *w.PriceChangePct != "0" {
					t.Errorf("window[%d]=%+v", i, w)
				}
			}
			addresses := []string{}
			for _, s := range got.SameName {
				addresses = append(addresses, s.Address)
			}
			if want := []string{"0x0000000000000000000000000000000000000063", "0x0000000000000000000000000000000000000062"}; !reflect.DeepEqual(addresses, want) {
				t.Errorf("same_name=%v, want %v", addresses, want)
			}
			var failure api.ErrorBody
			getPG(t, f.handler(), "/api/v1/tokens/{chain}/{address}", "/api/v1/tokens/bsc/0x00000000000000000000000000000000000000ff", 404, &failure)
		})
	}
}
