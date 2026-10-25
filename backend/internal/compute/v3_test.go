package compute_test

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

type fakeBalances struct {
	m     map[string]string // token hex -> balance
	asked []string
}

func (f *fakeBalances) BalanceOf(ctx context.Context, token, holder []byte) (*big.Int, error) {
	f.asked = append(f.asked, fmt.Sprintf("%s@%s", token, holder))
	v, ok := new(big.Int).SetString(f.m[string(token)], 10)
	if !ok {
		return nil, fmt.Errorf("no balance for %s", token)
	}
	return v, nil
}

func TestV3LiquiditySamplesPoolBalances(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	now := day.Add(12 * time.Hour)
	wbnb, meme := []byte("wbnb"), []byte("meme")
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "bsc", Asset: wbnb, Symbol: "BNB", Kind: "native", Decimals: 18}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "BNB", Minute: now.Truncate(time.Minute), USD: "600", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	eighteen := int16(18)
	v3 := func(id string) parse.Pool {
		return parse.Pool{ID: []byte(id), Token: meme, Quote: wbnb, QuoteSymbol: "BNB", Protocol: "pancake_v3", Dex: "pancake", Created: true, Status: "active"}
	}
	tr := trade(1, []byte("V3"), meme, "1000000000000000000", "1000000000000", "BNB")
	tr.PriceNative = "0.000001" // BNB per token
	apply(t, s, "bsc", 1, now.Add(-30*time.Second), parse.Facts{
		Tokens: []parse.Token{{Address: meme, Created: true, Symbol: "M", Decimals: &eighteen}},
		Pools:  []parse.Pool{v3("V3"), v3("V3-idle"), {ID: []byte("V2"), Token: meme, Quote: wbnb, QuoteSymbol: "BNB", Protocol: "pancake_v2", Dex: "pancake", Created: true, Status: "active"}},
		Trades: []parse.Trade{tr, func() parse.Trade { x := trade(2, []byte("V2"), meme, "1", "1", "BNB"); return x }()},
	})
	old := trade(1, []byte("V3-idle"), meme, "1", "1", "BNB")
	apply(t, s, "bsc", 0, now.Add(-2*time.Hour), parse.Facts{Trades: []parse.Trade{old}})

	// Balances recorded from the real pool 0xfad3…829a (chaininfo testdata).
	bal := &fakeBalances{m: map[string]string{"meme": "1972248601832277892231848", "wbnb": "3276432421007413848"}}
	v := compute.NewV3Liquidity(s, "bsc", []string{"pancake_v3"}, bal)
	if err := v.Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := compute.NewLiquidity(s, []compute.ChainNative{{Chain: "bsc", Symbol: "BNB"}}).Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	// 3.276432421007413848 BNB + 1972248.601832277892231848 tokens x 0.000001 BNB.
	want := "5.248681022839691740231848 3149.2086137038150441391088"
	if got := one(t, c, `SELECT trim_scale(liquidity_native)::text || ' ' || trim_scale(liquidity_usd)::text FROM pool_market WHERE pool_id = 'V3'`); got != want {
		t.Errorf("V3 liquidity (native usd) = %s; want %s", got, want)
	}
	if fmt.Sprint(bal.asked) != "[meme@V3 wbnb@V3]" {
		t.Errorf("balance calls = %v; want only the recently traded V3 pool", bal.asked)
	}
	// Within the same minute the pool is not asked again.
	bal.asked = nil
	if err := v.Step(ctx, now.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(bal.asked) != 0 {
		t.Errorf("second step balance calls = %v; want none", bal.asked)
	}
}
