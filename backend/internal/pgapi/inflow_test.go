package pgapi_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func inflowFixture(t *testing.T, publish bool) *fixture {
	t.Helper()
	f := newFixture(t)
	queueHolderMembers(t, f, "chain,address\nbsc,0x0000000000000000000000000000000000000011\nbase,0x0000000000000000000000000000000000000011\n")
	buildHolderMembers(t, f)
	for _, chain := range []string{"bsc", "base", "solana"} {
		cutoff := apiTime
		if chain == "base" {
			cutoff = cutoff.Add(-time.Minute)
		}
		first := cutoff.Add(-6 * time.Minute)
		six := int16(6)
		facts := parse.Facts{}
		changes := []store.MainPoolChange{}
		tails := []byte{0x61, 0x62, 0x63}
		if chain == "base" {
			tails = []byte{0x64}
		}
		if chain == "solana" {
			tails = nil
		}
		for i, tail := range tails {
			token, pool := raw(20, tail), raw(20, tail+0x40)
			platform, dex := "four_meme", "pancakeswap_v2"
			if i == 1 {
				dex = "four_meme_curve"
			}
			if chain == "base" {
				platform = "clanker"
				dex = "uniswap_v4"
			}
			facts.Tokens = append(facts.Tokens, parse.Token{Address: token, Created: true, Symbol: fmt.Sprintf("T%d", tail), Name: fmt.Sprintf("Token %d", tail), Decimals: &six, TotalSupply: "1000000000", LaunchPlatform: platform})
			facts.Pools = append(facts.Pools, parse.Pool{ID: pool, Token: token, Quote: raw(20, 0xc9), QuoteSymbol: "USDC", Protocol: "test", Dex: dex, Created: true, Status: "active"})
			changes = append(changes, store.MainPoolChange{Token: token, Pool: pool, Reason: "liquidity"})
		}
		f.apply(t, chain, 1, first, facts)
		swaps := parse.Facts{}
		for i, tail := range tails {
			amount := i + 3
			wallet := byte(0x11)
			if tail == 0x63 {
				wallet = 0x12
			}
			if chain == "base" {
				amount = 7
			}
			tr := parse.Trade{Tx: i + 1, Ix: 1, TxHash: raw(32, byte(i+1)), Token: raw(20, tail), Pool: raw(20, tail+0x40), Wallet: raw(20, wallet), Attribution: "confirmed", Side: "buy", TokenAmount: "1000000", QuoteAmount: fmt.Sprint(amount * 1000000), QuoteSymbol: "USDC", PriceNative: fmt.Sprint(amount), Decoder: "test", Version: 1}
			if tail == 0x61 {
				tr.QuoteAmount = "1000000"
				tr.PriceNative = "1"
				second := tr
				second.Tx = 10
				second.TxHash = raw(32, 10)
				second.QuoteAmount = "2000000"
				second.PriceNative = "2"
				swaps.Trades = append(swaps.Trades, second)
			}
			swaps.Trades = append(swaps.Trades, tr)
		}
		at := cutoff.Add(-4 * time.Minute)
		f.apply(t, chain, 2, at, swaps)
		f.apply(t, chain, 3, cutoff.Add(31*time.Second), parse.Facts{})
		native, price := "BNB", "100"
		if chain == "base" {
			native = "ETH"
			price = "2000"
		}
		if chain == "solana" {
			native = "SOL"
		}
		for _, err := range []error{
			f.st.SaveQuoteAssets(t.Context(), []store.QuoteAsset{{Chain: chain, Asset: raw(20, 0xc9), Symbol: "USDC", Kind: "stable", Decimals: 6}}),
			f.st.SaveQuotePrices(t.Context(), []store.QuotePrice{{Symbol: native, Minute: at, USD: price, Source: "binance"}}),
			f.st.FillValues(t.Context(), chain, native, first), f.st.SaveMainPools(t.Context(), chain, changes, first), f.st.UpdateMarket(t.Context(), chain, f.now, false),
			f.st.RefreshStats(t.Context(), chain, f.now), f.st.RefreshSmTrades(t.Context(), chain, f.now), f.st.RefreshCoverage(t.Context(), chain),
		} {
			if err != nil {
				t.Fatal(err)
			}
		}
		if publish {
			if _, err := f.st.RebuildInflow(t.Context(), chain, "5m", f.now); err != nil {
				t.Fatal(err)
			}
		}
	}
	return f
}

// H4-10 observes only HTTP responses, validated against the frozen schema.
// The independent facts mean A/B/C have 2/1/0 smart trades; A has $3/0.03 BNB.
func TestPgInflowEndToEnd(t *testing.T) {
	f := inflowFixture(t, true)
	h := f.handler()
	route := "/api/v1/smart-money/inflow"
	var first api.InflowPage
	getPG(t, h, route, route+"?chain=bsc&window=5m&limit=1", 200, &first)
	if first.Total != 3 || len(first.Items) != 1 || first.Items[0].Token.Address != "0x0000000000000000000000000000000000000061" || first.Items[0].SMTrades == nil || *first.Items[0].SMTrades != 2 || first.ListVersion != "2" || first.AsOf != "2026-10-23T10:00:00.000Z" || first.NextOffset == nil || *first.NextOffset != 1 {
		t.Fatalf("first page=%+v; want total3 A smart2 list2 as_of10:00 next1", first)
	}
	var next api.InflowPage
	getPG(t, h, route, route+"?query_id="+url.QueryEscape(first.QueryID)+"&offset=1&limit=1&chain=base&sort=price&order=asc", 200, &next)
	if len(next.Items) != 1 || next.Items[0].Token.Address != "0x0000000000000000000000000000000000000062" {
		t.Fatalf("frozen second page=%+v; want B", next)
	}
	var filtered api.InflowPage
	getPG(t, h, route, route+"?chain=bsc&window=5m&dex=pancakeswap_v2&min_volume=4", 200, &filtered)
	if filtered.Total != 1 || filtered.Items[0].Token.Address != "0x0000000000000000000000000000000000000063" {
		t.Fatalf("filter result=%+v; want only C ($5)", filtered)
	}
	var native api.InflowPage
	getPG(t, h, route, route+"?chain=bsc&window=5m&currency=native&limit=1", 200, &native)
	row := native.Items[0]
	if row.Currency != "BNB" || row.Volume == nil || *row.Volume != "0.03" || row.NetInflow == nil || *row.NetInflow != "0.03" || row.Price == nil || *row.Price != "0.02" {
		t.Fatalf("native row=%+v; want BNB volume/net0.03 price0.02", row)
	}
	var all api.InflowPage
	getPG(t, h, route, route+"?window=5m", 200, &all)
	if all.Total != 4 || len(all.ChainAsOf) != 3 || all.ChainAsOf[api.Base] != "2026-10-23T09:59:00.000Z" || all.ChainAsOf[api.BSC] != "2026-10-23T10:00:00.000Z" || all.AsOf != "2026-10-23T10:00:00.000Z" {
		t.Fatalf("all chain page=%+v; want 4 rows with separate cutoffs", all)
	}
	body, _ := json.Marshal(api.BatchRequest{QueryID: first.QueryID, ListVersion: first.ListVersion, Window: "5m", Currency: "usd", Tokens: []api.TokenKey{{Chain: api.BSC, Address: first.Items[0].Token.Address}}})
	var batch api.Batch
	listRequest(t, h, "POST", "/api/v1/tokens/batch", "/api/v1/tokens/batch", string(body), "", 200, &batch)
	if len(batch.Items) != 1 || batch.Items[0].NetInflow == nil || *batch.Items[0].NetInflow != "3" || batch.ListVersion != "2" {
		t.Fatalf("batch=%+v; want A net3 list2", batch)
	}
	if _, err := holderBackend(f).Exclude(t.Context(), api.BSC, "0x0000000000000000000000000000000000000011", "exclude for lifecycle"); err != nil {
		t.Fatal(err)
	}
	buildHolderMembers(t, f)
	listRequest(t, h, "POST", "/api/v1/tokens/batch", "/api/v1/tokens/batch", string(body), "", 410, nil)
	// New queries wait for the rebuild rather than label old rows with new members.
	getPG(t, h, route, route+"?chain=bsc&window=5m", 503, nil)
	if _, err := f.st.RebuildInflow(t.Context(), "bsc", "5m", f.now); err != nil {
		t.Fatal(err)
	}
	var fresh api.InflowPage
	getPG(t, h, route, route+"?chain=bsc&window=5m", 200, &fresh)
	if fresh.ListVersion != "3" || fresh.Total != 3 {
		t.Fatalf("fresh page=%+v, want list3 total3", fresh)
	}
	for _, row := range fresh.Items {
		if row.SMTrades == nil || *row.SMTrades != 0 || row.NetInflow == nil || *row.NetInflow != "0" {
			t.Errorf("excluded member contributed after activation: %+v", row)
		}
	}
	getPG(t, h, route, route+"?query_id="+url.QueryEscape(first.QueryID)+"&offset=0&limit=1", 200, &next)
	if next.ListVersion != "2" || next.Items[0].SMTrades == nil || *next.Items[0].SMTrades != 2 {
		t.Fatalf("old frozen page changed: %+v", next)
	}
}
