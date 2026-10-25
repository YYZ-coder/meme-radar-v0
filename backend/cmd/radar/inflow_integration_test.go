package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

// Regression at the compute command boundary: hold the serial pass in its
// price request, publish independently using real PG, then cancel both loops.
func TestComputePublishesInflowWhileSerialPassWaitsAndCancels(t *testing.T) {
	url := testdb.New(t)
	st, err := store.Open(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	cutoff := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	for n, at := range []time.Time{cutoff.Add(-2 * time.Hour), cutoff.Add(-time.Minute), cutoff.Add(time.Second)} {
		f := parse.Facts{}
		if n == 1 {
			six := int16(6)
			f.Tokens = []parse.Token{{Address: []byte("command-token"), Decimals: &six, Created: true}}
			f.Pools = []parse.Pool{{ID: []byte("command-pool"), Token: []byte("command-token"), Quote: []byte("command-quote"), Protocol: "test", Dex: "test", Created: true}}
			f.Trades = []parse.Trade{{Tx: 1, Ix: 1, TxHash: []byte("command-tx"), Token: []byte("command-token"), Pool: []byte("command-pool"), Wallet: []byte("command-wallet"), Attribution: "confirmed", Side: "buy", TokenAmount: "1000000", QuoteAmount: "1000000", QuoteSymbol: "BNB", PriceNative: "1", Decoder: "test", Version: 1}}
		}
		num := uint64(n + 1)
		if err = st.Commit(t.Context(), store.Commit{Chain: "bsc", Block: store.Block{Num: num, Hash: []byte{byte(num)}, ParentHash: []byte{byte(num - 1)}, Time: at, Status: store.StatusConfirmed, Payload: []byte(`{}`), Source: "test"}}); err != nil {
			t.Fatal(err)
		}
		if err = st.ApplyParsed(t.Context(), store.Parsed{Chain: "bsc", Num: num, Time: at, Version: 1, Facts: f}); err != nil {
			t.Fatal(err)
		}
	}
	for _, err := range []error{st.RefreshStats(t.Context(), "bsc", cutoff), st.RefreshSmTrades(t.Context(), "bsc", cutoff), st.RefreshCoverage(t.Context(), "bsc")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	called := make(chan struct{}, 1)
	priceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case called <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer priceServer.Close()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cmd := computeCmd{dbURL: url, interval: time.Hour, chains: []config.Chain{{Name: "bsc", Family: "evm", Native: "BNB", WrappedNative: "0x0000000000000000000000000000000000000098", LookupRPS: 1}}, prices: config.Prices{Sources: []config.PriceSource{{Type: "binance", URL: priceServer.URL}}}, inflow: cfg.Compute}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runCompute(ctx, cmd, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	select {
	case <-called:
	case err := <-done:
		t.Fatalf("compute stopped before serial price request: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("serial price request never started")
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	published := false
	for !published {
		var count int
		if err = conn.QueryRow(t.Context(), `SELECT count(*) FROM projection_heads h JOIN inflow_rows r ON r.projection_version=h.projection_version AND r.chain=h.chain AND r."window"=h."window" WHERE h.chain='bsc' AND h."window"='1h'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			published = true
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("independent inflow did not publish while serial pass was blocked")
		case <-poll.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancel compute=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("compute did not join cancelled projection loop")
	}
}
