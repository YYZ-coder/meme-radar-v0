package enrich_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/enrich"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

type fakeSupply struct{ asked []string }

func (f *fakeSupply) Supply(ctx context.Context, token []byte) (string, int16, error) {
	f.asked = append(f.asked, string(token))
	if string(token) == "D" {
		return "", 0, errors.New("execution reverted")
	}
	return "1000000000000000", 6, nil // 1e9 tokens
}

func TestEnrichFillsMissingSupplyForTradedTokens(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(ctx) })
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	wsol := []byte("wsol")
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: wsol, Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: now.Add(-time.Hour), USD: "100", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	block := func(num uint64, at time.Time, tokens ...string) {
		t.Helper()
		if err := s.Commit(ctx, store.Commit{Chain: "solana", Block: store.Block{Num: num, Hash: []byte{byte(num)}, ParentHash: []byte{byte(num - 1)},
			Time: at, Status: store.StatusConfirmed, TxCount: 1, Payload: []byte(`{}`), Source: "test"}}); err != nil {
			t.Fatal(err)
		}
		var f parse.Facts
		for i, tok := range tokens {
			tk := parse.Token{Address: []byte(tok), Decimals: &six}
			if tok == "B" {
				tk.Created, tk.TotalSupply = true, "5000000" // supply known from its creation event
			}
			pool := []byte("p" + tok)
			f.Tokens = append(f.Tokens, tk)
			f.Pools = append(f.Pools, parse.Pool{ID: pool, Token: []byte(tok), Quote: wsol, QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"})
			f.Trades = append(f.Trades, parse.Trade{Tx: i, Ix: 1, TxHash: []byte{byte(i)}, Pool: pool, Token: []byte(tok), Wallet: []byte("w"),
				Attribution: "confirmed", Side: "buy", TokenAmount: "1000000", QuoteAmount: "1000000000", QuoteSymbol: "SOL",
				PriceNative: "1", ValueNative: "1", Decoder: "test", Version: 1})
		}
		if err := s.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: num, Time: at, Version: 1, Facts: f}); err != nil {
			t.Fatal(err)
		}
	}
	block(1, now.Add(-48*time.Hour), "C") // not traded in the last 24 hours
	block(2, now.Add(-time.Hour), "A", "B", "D")

	sup := &fakeSupply{}
	e := enrich.New(s, "solana", sup)
	okBefore := testutil.ToFloat64(metrics.EnrichLookups.WithLabelValues("solana", "ok"))
	errBefore := testutil.ToFloat64(metrics.EnrichLookups.WithLabelValues("solana", "error"))
	defer func() {
		if ok, bad := testutil.ToFloat64(metrics.EnrichLookups.WithLabelValues("solana", "ok"))-okBefore,
			testutil.ToFloat64(metrics.EnrichLookups.WithLabelValues("solana", "error"))-errBefore; ok != 1 || bad != 2 {
			t.Errorf("lookups reported ok %v error %v; want 1 and 2", ok, bad)
		}
	}()
	if err := e.Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(sup.asked) != "[A D]" {
		t.Errorf("asked %v; want [A D]: traded tokens without a creation-event supply", sup.asked)
	}
	got := func(sql string) string {
		var v string
		if err := db.QueryRow(ctx, sql).Scan(&v); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return v
	}
	if g := got(`SELECT string_agg(address::text || ' ' || coalesce(total_supply::text, '-') || ' ' || coalesce(decimals::text, '-') || ' ' || coalesce(error, '-'), ', ' ORDER BY address)
		FROM token_supplements`); g != `\x41 1000000000000000 6 -, \x44 - - execution reverted` {
		t.Errorf("supplements = %s", g)
	}
	// Nothing to do 10 minutes later; the failed token is retried after an hour.
	sup.asked = nil
	e.Step(ctx, now.Add(10*time.Minute))
	e.Step(ctx, now.Add(61*time.Minute))
	if fmt.Sprint(sup.asked) != "[D]" {
		t.Errorf("later asks %v; want [D] once, after an hour", sup.asked)
	}
	// compute takes the supplement when the creation event is missing:
	// A at 100 USD x 1e9 tokens; B keeps its creation-event supply (5 tokens).
	chains := []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}
	if err := compute.NewValues(s, chains).Step(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := compute.NewMarket(s, chains).Step(ctx, now, true); err != nil {
		t.Fatal(err)
	}
	if g := got(`SELECT string_agg(address::text || ' ' || coalesce(trim_scale(market_cap_usd)::text, '-'), ', ' ORDER BY address) FROM token_market`); g != `\x41 100000000000, \x42 500, \x44 -` {
		t.Errorf("market caps = %s; want A from the supplement, B from its creation event, D unknown", g)
	}
}
