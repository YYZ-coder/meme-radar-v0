package pgapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/balances"
	"github.com/meme-radar/radar/internal/solana"
	"github.com/meme-radar/radar/internal/store"
)

const solFixture = "GX1JGwFmrzgkv6MBjwZZdZt3qBcWi1d5ZuAUS3KFpump"
const baseFixture = "0x7d4bfd446d4b81d5ede62529f9c8db6fdd81e222"

// widestFixture uses formal writers on all chains, including closed candles.
// The two extra token addresses are known mock tokens so shape tests send the
// same requests to both backends. All amounts remain independent toy facts.
func widestFixture(t *testing.T, fullHistory ...bool) *fixture {
	t.Helper()
	f := detailFixture(t, false)
	ctx := context.Background()
	for _, tc := range []struct {
		chain, native, address string
		size                   int
	}{{"solana", "SOL", solFixture, 32}, {"base", "ETH", baseFixture, 20}} {
		var token []byte
		if tc.chain == "solana" {
			key, err := solana.ParseKey(tc.address)
			if err != nil {
				t.Fatal(err)
			}
			token = key[:]
		} else {
			key, err := evm.ParseAddress(tc.address)
			if err != nil {
				t.Fatal(err)
			}
			token = key[:]
		}
		pool, quote := raw(tc.size, 0xa9), raw(tc.size, 0xc9)
		six := int16(6)
		hashSize := 32
		if tc.chain == "solana" {
			hashSize = 64
		}
		at := apiTime.Add(-time.Minute)
		first := uint64(1)
		if len(fullHistory) > 0 && fullHistory[0] {
			// A previously parsed empty prefix covers the UI's 300 x 5m query.
			// This uses the same formal block/parse writers as the data facts.
			f.apply(t, tc.chain, first, apiTime.Add(-26*time.Hour), parse.Facts{})
			first++
		}
		f.apply(t, tc.chain, first, at, parse.Facts{Tokens: []parse.Token{{Address: token, Created: true, Symbol: "GX1J", Name: "Fixture", Decimals: &six, TotalSupply: "1000000000000000"}}, Pools: []parse.Pool{{ID: pool, Token: token, Quote: quote, QuoteSymbol: "USDC", Protocol: "uniswap_v4", Dex: "fixture", Created: true, ReserveToken: "1000000", ReserveQuote: "1000000"}},
			Trades:    []parse.Trade{{Tx: 1, Ix: 1, TxHash: raw(hashSize, 1), Pool: pool, Token: token, Wallet: raw(tc.size, 3), Attribution: "confirmed", Side: "buy", TokenAmount: "1000000", QuoteAmount: "3000", QuoteSymbol: "USDC", PriceNative: "0.003", Decoder: "test", Version: 1}},
			Liquidity: []parse.Liquidity{{Tx: 2, Ix: 1, TxHash: raw(hashSize, 2), Pool: pool, Token: token, Wallet: raw(tc.size, 4), Kind: "add", TokenAmount: "1000000", QuoteAmount: "1000000", QuoteSymbol: "USDC", Decoder: "test", Version: 1}},
		})
		f.apply(t, tc.chain, first+1, f.now, parse.Facts{})
		for _, step := range []func() error{
			func() error {
				return f.st.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: tc.chain, Asset: quote, Symbol: "USDC", Kind: "stable", Decimals: 6}})
			},
			func() error {
				return f.st.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: tc.native, Minute: at, USD: "100", Source: "coinbase"}})
			},
			func() error { return f.st.FillValues(ctx, tc.chain, tc.native, at) },
			func() error { return f.st.SampleLiquidity(ctx, tc.chain) },
			func() error { return f.st.PriceLiquidity(ctx, tc.chain, tc.native, f.now) },
			func() error {
				return f.st.SaveMainPools(ctx, tc.chain, []store.MainPoolChange{{Token: token, Pool: pool, Reason: "liquidity"}}, at)
			},
			func() error { return f.st.UpdateMarket(ctx, tc.chain, f.now, false) },
			func() error { return f.st.RefreshStats(ctx, tc.chain, f.now) },
			func() error { return f.st.RefreshCoverage(ctx, tc.chain) },
			func() error { return f.st.RefreshATH(ctx, tc.chain) },
		} {
			if err := step(); err != nil {
				t.Fatal(err)
			}
		}
	}
	var last uint64
	if err := f.conn.QueryRow(ctx, "SELECT max(block_num) FROM chain_blocks WHERE chain='bsc'").Scan(&last); err != nil {
		t.Fatal(err)
	}
	changes := []parse.Liquidity{}
	for i := 0; i < 3; i++ {
		changes = append(changes, parse.Liquidity{Tx: 100 + i, Ix: 1, TxHash: raw(32, byte(100+i)), Pool: raw(20, 0xa6), Token: raw(20, 0x61), Wallet: raw(20, 0xde), Kind: "add", TokenAmount: "1000000", QuoteAmount: "1000000000000000000", QuoteSymbol: "BNB", Decoder: "test", Version: 1})
	}
	f.now = apiTime.Add(40 * time.Second)
	f.apply(t, "bsc", last+1, apiTime.Add(35*time.Second), parse.Facts{Liquidity: changes})

	return f
}

func contractHoldings(t *testing.T, f *fixture) {
	t.Helper()
	ctx := context.Background()
	// H5 contract routes exercise real non-empty balances on every chain.
	for _, chain := range []string{"bsc", "base", "solana"} {
		var token []byte
		switch chain {
		case "bsc":
			token = raw(20, 0x61)
		case "base":
			key := evm.MustAddress(baseFixture)
			token = key[:]
		case "solana":
			key, err := solana.ParseKey(solFixture)
			if err != nil {
				t.Fatal(err)
			}
			token = key[:]
		}
		blocks, err := f.st.UnbalancedBlocks(ctx, chain, balances.Version, 100)
		if err != nil {
			t.Fatal(err)
		}
		for i, block := range blocks {
			b := store.BalanceBlock{Chain: chain, Num: block.Num, Time: block.Time, Version: balances.Version}
			if i == 0 {
				for n, amount := range []string{"30000000", "20000000", "10000000"} {
					b.Deltas = append(b.Deltas, parse.Balance{Token: token, Owner: raw(len(token), byte(0x11+n)), Delta: amount})
				}
			}
			if err := f.st.ApplyBalances(ctx, []store.BalanceBlock{b}); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.st.RefreshCoverage(ctx, chain); err != nil {
			t.Fatal(err)
		}
		if err := f.st.RefreshBalances(ctx, chain, f.now, balances.Version); err != nil {
			t.Fatal(err)
		}
	}
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	t.Cleanup(rpc.Close)
	for i := range f.cfg.Chains {
		f.cfg.Chains[i].LookupRPCURL = rpc.URL
		f.cfg.Chains[i].LookupRPS = 1000
	}

}

func TestPgEveryRouteMatchesContract(t *testing.T) {
	f := widestFixture(t)
	contractHoldings(t, f)
	h := f.handler()
	failureID := f.failure(t, "bsc", 90000, 1, "four_meme", f.now.Add(-time.Minute))
	requests := append(h3ContractRoutes(t, f, placeholderRoutes()), []routeRequest{
		{"GET", "/api/v1/admin/health", "/api/v1/admin/health", "", 200},
		{"POST", "/api/v1/admin/parse-failures/{id}/retry", fmt.Sprintf("/api/v1/admin/parse-failures/%d/retry", failureID), "", 202},
		{"GET", "/healthz", "/healthz", "", 200},
		{"GET", "/api/v1/market/natives", "/api/v1/market/natives", "", 200},
		{"GET", "/api/v1/search", "/api/v1/search?q=DEMO", "", 200},
	}...)
	const route = "/api/v1/tokens/{chain}/{address}"
	const bsc = "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061"
	for _, target := range []string{bsc, "/api/v1/tokens/solana/" + solFixture, "/api/v1/tokens/base/" + baseFixture} {
		for _, suffix := range []string{"", "/activities", "/holders", "/pools", "/liquidity-events"} {
			requests = append(requests, routeRequest{"GET", route + suffix, target + suffix, "", 200})
		}
		requests = append(requests, routeRequest{"GET", route, target + "?currency=native", "", 200})
		for _, interval := range []string{"1s", "30s", "1m", "5m", "1h", "1d"} {
			for _, currency := range []string{"usd", "native"} {
				requests = append(requests, routeRequest{"GET", route + "/candles", fmt.Sprintf("%s/candles?interval=%s&currency=%s&limit=2", target, interval, currency), "", 200})
			}
		}
	}
	covered := map[string]bool{}
	for _, c := range requests {
		t.Run(c.method+" "+c.target, func(t *testing.T) {
			rec := requestPG(t, h, c)
			if c.route == holdersRoute {
				var got api.Holders
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Total == nil || *got.Total != 3 || len(got.Items) != 3 || got.Coverage.Status != "full" {
					t.Errorf("real H5 contract holders=%+v", got)
				}
			}
		})
		covered[c.method+" "+c.route] = true
	}
	// Contract inventory is authoritative: a new operation needs an explicit
	// representative request; returning some schema-valid error cannot hide it.
	for path, item := range contracttest.Spec(t).Paths.Map() {
		for method := range item.Operations() {
			if !covered[strings.ToUpper(method)+" "+path] {
				t.Errorf("operation has no PG request: %s %s", method, path)
			}
		}
	}
	for _, suffix := range []string{"/activities", "/holders", "/liquidity-events"} {
		t.Run("second page "+suffix, func(t *testing.T) {
			rec := requestPG(t, h, routeRequest{"GET", route + suffix, bsc + suffix + "?limit=2", "", 200})
			var body struct {
				NextCursor *string `json:"next_cursor"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.NextCursor == nil {
				t.Fatal("fixture must exercise the second page")
			}
			requestPG(t, h, routeRequest{http.MethodGet, route + suffix, bsc + suffix + "?limit=2&cursor=" + url.QueryEscape(*body.NextCursor), "", 200})
		})
	}
	t.Run("H5 active smart-money card", func(t *testing.T) {
		sf := smartMoneyHoldingsFixture(t)
		var card api.TokenSmartMoney
		getPG(t, sf.handler(), route+"/smart-money", bsc+"/smart-money", 200, &card)
		if card.ListVersion != "3" || card.Holders == nil || *card.Holders != 1 || len(card.Items) != 1 || card.Items[0].Balance == nil || *card.Items[0].Balance != "100000000" {
			t.Fatalf("real smart-money contract=%+v", card)
		}
		if card.Items[0].BuyVolumeUSD != nil || card.Items[0].SellVolumeUSD != nil {
			t.Errorf("H4 absent: %+v", card.Items[0])
		}
	})

}
