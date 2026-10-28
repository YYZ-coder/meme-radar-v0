package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/enrich"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

func TestEnrichPrivateRPCPersistsKnownSupply(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.New(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	token, pool, quote := bytes.Repeat([]byte{6}, 32), bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)
	if err := st.Commit(ctx, store.Commit{Chain: "solana", Block: store.Block{Num: 1, Hash: []byte{1}, Time: now.Add(-time.Minute), Status: store.StatusConfirmed, Payload: []byte("{}"), Source: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyParsed(ctx, store.Parsed{Chain: "solana", Num: 1, Time: now.Add(-time.Minute), Version: 1, Facts: parse.Facts{
		Tokens: []parse.Token{{Address: token}},
		Pools:  []parse.Pool{{ID: pool, Token: token, Quote: quote, QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap"}},
		Trades: []parse.Trade{{Tx: 1, Ix: 1, TxHash: []byte{1}, Pool: pool, Token: token, Attribution: "ambiguous", Side: "buy", TokenAmount: "1", QuoteAmount: "1", Decoder: "fixture", Version: 1}},
	}}); err != nil {
		t.Fatal(err)
	}
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"context":{"slot":1},"value":{"amount":"123456000000","decimals":6}}}`)
	}))
	defer primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer public.Close()
	t.Setenv("RADAR_TEST_ENRICH_RPC_URL", primary.URL)
	chain := config.Chain{Name: "solana", Family: "solana", RPCURLEnv: "RADAR_TEST_ENRICH_RPC_URL", LookupRPCURL: public.URL, LookupRPS: 1000}
	worker := enrich.New(st, "solana", supplyLookup(chain, st, enrichRPC(chain)))
	if err := worker.Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	detail, found, err := st.APIToken(ctx, "solana", token, false)
	if err != nil || !found {
		t.Fatalf("token lookup found=%v err=%v", found, err)
	}
	// The RPC sample supplies 123456000000 atomic units with six decimals.
	if detail.Decimals == nil || *detail.Decimals != 6 || detail.Supply == nil || *detail.Supply != "123456" {
		t.Fatalf("persisted token supply=%v decimals=%v; want 123456 whole units and 6 decimals from private RPC", detail.Supply, detail.Decimals)
	}
}
