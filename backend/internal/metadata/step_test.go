package metadata

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/solana"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

var metadataDay = time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)

func metadataDB(t *testing.T) (*store.Store, *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	dbURL := testdb.New(t)
	st, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(context.Background()) })
	return st, db
}

// Token facts and trades go through the same transaction path as parse.
func metadataFacts(t *testing.T, st *store.Store, chain string, num uint64, at time.Time, tokens []parse.Token, traded bool) {
	t.Helper()
	ctx := context.Background()
	if err := st.Commit(ctx, store.Commit{Chain: chain, Block: store.Block{Num: num, Hash: []byte{byte(num)}, ParentHash: []byte{byte(num - 1)}, Time: at, Status: store.StatusConfirmed, TxCount: 1, Payload: []byte(`{}`), Source: "test"}}); err != nil {
		t.Fatal(err)
	}
	facts := parse.Facts{Tokens: tokens}
	if traded {
		for i, tk := range tokens {
			pool := append([]byte("pool"), tk.Address...)
			facts.Pools = append(facts.Pools, parse.Pool{ID: pool, Token: tk.Address, Quote: []byte("quote"), QuoteSymbol: "SOL", Protocol: "test", Dex: "test", Status: "active", Created: true})
			facts.Trades = append(facts.Trades, parse.Trade{Tx: i, Ix: 1, TxHash: []byte{byte(i)}, Pool: pool, Token: tk.Address, Wallet: []byte("wallet"), Attribution: "confirmed", Side: "buy", TokenAmount: "1", QuoteAmount: "1", QuoteSymbol: "SOL", Decoder: "test", Version: 1})
		}
	}
	if err := st.ApplyParsed(ctx, store.Parsed{Chain: chain, Num: num, Time: at, Version: 1, Facts: facts}); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataStepSchedulesAndBacksOff(t *testing.T) {
	st, db := metadataDB(t)
	ctx := context.Background()
	now := metadataDay.Add(12 * time.Hour)
	token := func(n byte) []byte { return bytes.Repeat([]byte{n}, 32) }
	ok, bad, gateway, blocked := token(1), token(2), token(3), token(4)
	metadataFacts(t, st, "solana", 1, now.Add(-time.Minute), []parse.Token{
		{Address: ok, URI: "https://metadata.example/ok"},
		{Address: bad, URI: "https://metadata.example/fail"},
		{Address: gateway, URI: "https://original.example/ipfs/bafkreieiqmlygrwswplz6boqi6zrwd6a2d6u52dm6lugw7ibe6kquw63vm"},
		{Address: blocked, URI: "http://localhost:3000/private"},
	}, true)
	hits := map[string]int{}
	f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		if r.URL.Path == "/fail" || strings.HasPrefix(r.URL.Path, "/ipfs/") {
			w.WriteHeader(403)
			return
		}
		w.Write([]byte(`{"image":"https://images.example/token.png","twitter":"@example"}`))
	})
	f.options.IPFSGateways = []string{"https://first.example/ipfs/", "https://second.example/ipfs/"}
	clockNow := now
	f.options.Now = func() time.Time { return clockNow }
	worker := New(st, "solana", f, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	beforeOK := testutil.ToFloat64(metrics.MetadataFetches.WithLabelValues("solana", "ok"))
	beforeBad := testutil.ToFloat64(metrics.MetadataFetches.WithLabelValues("solana", "failed"))
	beforeBlocked := testutil.ToFloat64(metrics.MetadataFetches.WithLabelValues("solana", "blocked"))
	if err := worker.Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM token_metadata`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("metadata rows=%d; want 4 due-token outcomes", count)
	}
	var image, status string
	var attempts int
	if err := db.QueryRow(ctx, `SELECT image_url,status,attempts FROM token_metadata WHERE chain='solana' AND address=$1`, ok).Scan(&image, &status, &attempts); err != nil {
		t.Fatal(err)
	}
	if image != "https://images.example/token.png" || status != "ok" || attempts != 1 {
		t.Errorf("ok token image=%q status=%s attempts=%d", image, status, attempts)
	}
	if testutil.ToFloat64(metrics.MetadataFetches.WithLabelValues("solana", "ok"))-beforeOK != 1 || testutil.ToFloat64(metrics.MetadataFetches.WithLabelValues("solana", "failed"))-beforeBad != 2 || testutil.ToFloat64(metrics.MetadataFetches.WithLabelValues("solana", "blocked"))-beforeBlocked != 1 {
		t.Error("initial metadata outcome metric deltas must be ok=1 failed=2 blocked=1")
	}
	// Independent schedule arithmetic: attempt offsets are 0, 10m, 70m,
	// 430m, 1870m. A fifth failure is terminal, with no next attempt.
	for i, offset := range []time.Duration{0, 10 * time.Minute, 70 * time.Minute, 430 * time.Minute, 1870 * time.Minute} {
		at := now.Add(offset)
		clockNow = at
		if i > 0 {
			if err := worker.Step(ctx, at); err != nil {
				t.Fatal(err)
			}
		}
		var next *time.Time
		var attempt int
		var state string
		if err := db.QueryRow(ctx, `SELECT attempts,next_attempt_at,status FROM token_metadata WHERE chain='solana' AND address=$1`, bad).Scan(&attempt, &next, &state); err != nil {
			t.Fatal(err)
		}
		if attempt != i+1 || state != "failed" {
			t.Errorf("attempt %d: got attempts=%d state=%s", i+1, attempt, state)
		}
		if i < 4 {
			want := now.Add([]time.Duration{10 * time.Minute, 70 * time.Minute, 430 * time.Minute, 1870 * time.Minute}[i])
			if next == nil || !next.Equal(want) {
				t.Errorf("attempt %d next=%v; want %v", i+1, next, want)
			}
			if err := worker.Step(ctx, want.Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
		} else if next != nil {
			t.Errorf("fifth failure next=%v; want NULL", next)
		}
	}
	if err := worker.Step(ctx, now.Add(100*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if hits["/fail"] != 5 {
		t.Errorf("failed-source requests=%d; want exactly five", hits["/fail"])
	}
	if hits["/ipfs/bafkreieiqmlygrwswplz6boqi6zrwd6a2d6u52dm6lugw7ibe6kquw63vm"] != 15 {
		t.Errorf("gateway failure requests=%v; want three gateways per attempt, five attempts", hits)
	}
	// Clanker is an image URI, not a JSON request, and chain facts stay intact.
	clanker := bytes.Repeat([]byte{9}, 20)
	metadataFacts(t, st, "base", 2, now.Add(-time.Minute), []parse.Token{{Address: clanker, Created: true, LaunchPlatform: "clanker", Name: "On-chain", Symbol: "CHAIN", URI: "ipfs://QmezC5igZgKXgZ4nDRd2c1jsJ4DSTz3egw9WX1PyFbomVr"}}, true)
	requestsBefore := 0
	for _, n := range hits {
		requestsBefore += n
	}
	if err := New(st, "base", f, nil, nil).Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	var onChainName, onChainSymbol string
	if err := db.QueryRow(ctx, `SELECT m.image_url,m.status,t.name,t.symbol FROM token_metadata m JOIN tokens t USING(chain,address) WHERE m.chain='base'`).Scan(&image, &status, &onChainName, &onChainSymbol); err != nil {
		t.Fatal(err)
	}
	if image != "https://first.example/ipfs/QmezC5igZgKXgZ4nDRd2c1jsJ4DSTz3egw9WX1PyFbomVr" || status != "ok" || onChainName != "On-chain" || onChainSymbol != "CHAIN" {
		t.Errorf("Clanker result %q %s, facts %s %s", image, status, onChainName, onChainSymbol)
	}
	requestsAfter := 0
	for _, n := range hits {
		requestsAfter += n
	}
	if requestsAfter != requestsBefore {
		t.Fatal("Clanker fetched an image")
	}
}

func TestMetadataStepFourMemeAndMissingSources(t *testing.T) {
	st, db := metadataDB(t)
	ctx := context.Background()
	now := metadataDay.Add(12 * time.Hour)
	address, _ := hex.DecodeString("5d709925a8671dbd97d73d0b1f3053ec66dfffff")
	missing := bytes.Repeat([]byte{7}, 20)
	metadataFacts(t, st, "bsc", 1, now.Add(-time.Minute), []parse.Token{{Address: address, Created: true, LaunchPlatform: "four_meme", Name: "Chain name", Symbol: "CHAIN"}, {Address: missing, LaunchPlatform: "four_meme"}}, true)
	sample, err := os.ReadFile("testdata/fourmeme_token_get_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("address") == "0x"+hex.EncodeToString(missing) {
			w.Write([]byte(`{"code":0,"msg":"success"}`))
			return
		}
		w.Write(sample)
	})
	f.options.FourMemeAPIURL = "https://four.example/get/v2"
	if err := New(st, "bsc", f, nil, nil).Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	var status, image, name, symbol string
	if err := db.QueryRow(ctx, `SELECT m.status,m.image_url,t.name,t.symbol FROM token_metadata m JOIN tokens t USING(chain,address) WHERE m.chain='bsc' AND m.address=$1`, address).Scan(&status, &image, &name, &symbol); err != nil {
		t.Fatal(err)
	}
	if status != "ok" || image != "https://static.four.meme/market/10234cg8Yq3jRu.png" || name != "Chain name" || symbol != "CHAIN" {
		t.Errorf("Four.meme %s %q; chain facts %s %s", status, image, name, symbol)
	}
	if err := db.QueryRow(ctx, `SELECT status FROM token_metadata WHERE chain='bsc' AND address=$1`, missing).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "skipped" {
		t.Errorf("missing API data status=%s; want skipped", status)
	}
	// No URI and no supported platform is a terminal skipped outcome.
	other := bytes.Repeat([]byte{8}, 20)
	metadataFacts(t, st, "base", 2, now.Add(-time.Minute), []parse.Token{{Address: other}}, false)
	if err := New(st, "base", f, nil, nil).Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT status FROM token_metadata WHERE chain='base' AND address=$1`, other).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "skipped" {
		t.Errorf("no-source status=%s", status)
	}
}

func TestMetadataStepPrioritizesRecentAndLimitsBatch(t *testing.T) {
	st, db := metadataDB(t)
	ctx := context.Background()
	now := metadataDay.Add(12 * time.Hour)
	cold := parse.Token{Address: bytes.Repeat([]byte{0}, 32), URI: "https://metadata.example/cold"}
	metadataFacts(t, st, "solana", 1, now.Add(-25*time.Hour), []parse.Token{cold}, true)
	var tokens []parse.Token
	for n := 1; n <= 100; n++ {
		tokens = append(tokens, parse.Token{Address: bytes.Repeat([]byte{byte(n)}, 32), URI: fmt.Sprintf("https://metadata.example/%d", n)})
	}
	metadataFacts(t, st, "solana", 2, now.Add(-time.Minute), tokens, true)
	f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	if err := New(st, "solana", f, nil, nil).Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	var total, coldCount int
	if err := db.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE address=$1) FROM token_metadata WHERE chain='solana'`, cold.Address).Scan(&total, &coldCount); err != nil {
		t.Fatal(err)
	}
	if total != 100 || coldCount != 0 {
		t.Errorf("processed=%d cold=%d; want recent 100 first, cold 0", total, coldCount)
	}
}

func TestMetadataStepResolvesMetaplexURI(t *testing.T) {
	st, db := metadataDB(t)
	ctx := context.Background()
	now := metadataDay.Add(12 * time.Hour)
	mint, err := solana.DecodeBase58("JUPyiwrYJFskUPiHa7hkeR8VUtAeFoSYbKedZNsDvCN")
	if err != nil {
		t.Fatal(err)
	}
	metadataFacts(t, st, "solana", 1, now.Add(-time.Minute), []parse.Token{{Address: mint}}, true)
	var recordings struct {
		Samples []struct{ Response json.RawMessage }
	}
	raw, err := os.ReadFile("testdata/metaplex_accounts.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &recordings); err != nil {
		t.Fatal(err)
	}
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(recordings.Samples[1].Response) }))
	defer rpc.Close()
	source := NewChainSource("solana", jsonrpc.New(rpc.URL, rpc.Client(), nil, nil))
	f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"image":"https://images.example/jup.png"}`))
	})
	if err := New(st, "solana", f, source, nil).Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	var uri, status string
	if err := db.QueryRow(ctx, `SELECT source_uri,status FROM token_metadata WHERE chain='solana' AND address=$1`, mint).Scan(&uri, &status); err != nil {
		t.Fatal(err)
	}
	if uri != "https://static.jup.ag/jup/metadata.json" || status != "ok" {
		t.Errorf("Metaplex pipeline URI=%q status=%s", uri, status)
	}
}
