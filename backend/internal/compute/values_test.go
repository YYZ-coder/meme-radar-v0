package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

func TestValuesStepPricesTradesAndLiquidity(t *testing.T) {
	ctx := context.Background()
	s, c := open(t)
	m0 := day.Add(10 * time.Hour)
	wsol, usdc := []byte("wsol"), []byte("usdc")
	wbnb, usdt := []byte("wbnb"), []byte("usdt")
	meme, other := []byte("meme"), []byte("unknown-quote")
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{
		{Chain: "solana", Asset: wsol, Symbol: "SOL", Kind: "native", Decimals: 9},
		{Chain: "solana", Asset: usdc, Symbol: "USDC", Kind: "stable", Decimals: 6},
		{Chain: "bsc", Asset: wbnb, Symbol: "BNB", Kind: "native", Decimals: 18},
		{Chain: "bsc", Asset: usdt, Symbol: "USDT", Kind: "stable", Decimals: 18},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{
		{Symbol: "SOL", Minute: m0, USD: "200", Source: "binance"},
		{Symbol: "BNB", Minute: m0, USD: "600", Source: "binance"},
	}); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	pool := func(id, quote []byte, sym string) parse.Pool {
		return parse.Pool{ID: id, Token: meme, Quote: quote, QuoteSymbol: sym, Protocol: "test", Dex: "test", Created: true, Status: "active"}
	}
	solFacts := parse.Facts{
		Tokens: []parse.Token{{Address: meme, Created: true, Symbol: "MEME", Decimals: &six}},
		Pools:  []parse.Pool{pool([]byte("p-sol"), wsol, "SOL"), pool([]byte("p-usdc"), usdc, "USDC"), pool([]byte("p-x"), other, "X")},
	}
	// 3 tokens for 1.5 SOL: 0.5 SOL per token.
	t1 := trade(1, []byte("p-sol"), meme, "3000000", "1500000000", "SOL")
	t1.PriceNative, t1.ValueNative = "0.5", "1.5"
	// 2.5 USDC for 1 token.
	t2 := trade(2, []byte("p-usdc"), meme, "1000000", "2500000", "USDC")
	t2.PriceNative = "2.5"
	t3 := trade(3, []byte("p-x"), meme, "1000000", "7", "X")
	solFacts.Trades = []parse.Trade{t1, t2, t3}
	// 1 SOL + 2 tokens added 50s later; the token's last price is 100 USD.
	solFacts.Liquidity = []parse.Liquidity{{Tx: 4, Ix: 1, TxHash: []byte{4}, Pool: []byte("p-sol"), Token: meme, Kind: "add",
		TokenAmount: "2000000", QuoteAmount: "1000000000", QuoteSymbol: "SOL", ValueNative: "1", Decoder: "test", Version: 1}}
	apply(t, s, "solana", 10, m0.Add(30*time.Second), solFacts)
	late := trade(0, []byte("p-sol"), meme, "1000000", "1000000000", "SOL")
	late.PriceNative, late.ValueNative = "1", "1"
	apply(t, s, "solana", 11, m0.Add(6*time.Minute+5*time.Second), parse.Facts{Trades: []parse.Trade{late}}) // SOL price 6 min old
	// 3 USDT (18 decimals) for 1 token on BSC.
	b1 := trade(0, []byte("p-usdt"), meme, "1000000000000000000", "3000000000000000000", "USDT")
	b1.PriceNative = "3"
	apply(t, s, "bsc", 20, m0.Add(10*time.Second), parse.Facts{
		Tokens: []parse.Token{{Address: meme, Created: true, Symbol: "MEME", Decimals: func() *int16 { v := int16(18); return &v }()}},
		Pools:  []parse.Pool{pool([]byte("p-usdt"), usdt, "USDT")}, Trades: []parse.Trade{b1}})

	v := compute.NewValues(s, []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}, {Chain: "bsc", Symbol: "BNB"}})
	if err := v.Step(ctx, m0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	row := func(table, chain string, num uint64, tx int) string {
		return one(t, c, `SELECT concat_ws(' ', coalesce(trim_scale(value_usd)::text, 'NULL'), coalesce(trim_scale(value_native)::text, 'NULL')`+
			map[string]string{"trades": `, coalesce(trim_scale(price_usd)::text, 'NULL')`, "liquidity_events": ""}[table]+
			`) FROM `+table+` WHERE chain = $1 AND block_num = $2 AND tx_index = $3`, chain, num, tx)
	}
	for _, w := range []struct {
		table, chain string
		num          uint64
		tx           int
		want         string // value_usd value_native [price_usd]
	}{
		{"trades", "solana", 10, 1, "300 1.5 100"},
		{"trades", "solana", 10, 2, "2.5 0.0125 2.5"},
		{"trades", "solana", 10, 3, "NULL NULL NULL"}, // quote asset not registered
		{"trades", "solana", 11, 0, "NULL 1 NULL"},    // native price stale (>5 min)
		{"trades", "bsc", 20, 0, "3 0.005 3"},
		{"liquidity_events", "solana", 10, 4, "400 2"},
	} {
		if got := row(w.table, w.chain, w.num, w.tx); got != w.want {
			t.Errorf("%s %s %d/%d = %s; want %s", w.table, w.chain, w.num, w.tx, got, w.want)
		}
	}
}
