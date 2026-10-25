package chaininfo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/meme-radar/radar/internal/chaininfo"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

func countingReplay(t *testing.T, calls *atomic.Int64) *chaininfo.RPC {
	t.Helper()
	b, _ := os.ReadFile("testdata/bsc_calls.json")
	var rec []struct {
		To, Data string
		Response json.RawMessage
	}
	json.Unmarshal(b, &rec)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct{ Params []json.RawMessage }
		json.NewDecoder(r.Body).Decode(&req)
		var call struct{ To, Data string }
		json.Unmarshal(req.Params[0], &call)
		for _, c := range rec {
			if strings.EqualFold(c.To, call.To) && strings.EqualFold(c.Data, call.Data) {
				w.Write(c.Response)
				return
			}
		}
		t.Errorf("unrecorded call %s %s", call.To, call.Data)
	}))
	t.Cleanup(srv.Close)
	return chaininfo.New(jsonrpc.New(srv.URL, srv.Client(), nil, nil), evm.MustAddress("0xF251F83e40a78868FcfA3FA4599Dad6494E46034"))
}

func TestCachedAsksTheChainOncePerToken(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.New(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	four, usdt := evm.MustAddress("0x3b7ef5fa9edf3b6b763768ac654d3a5b0d2d4444"), evm.MustAddress("0x55d398326f99059ff775485246999027b3197955")
	first := chaininfo.NewCached("bsc", st, countingReplay(t, &calls))
	a, err := first.FourMeme(ctx, four)
	if err != nil || !a.Found || a.Quote != usdt {
		t.Fatalf("FourMeme = %+v, %v; want found with quote USDT", a, err)
	}
	if n, err := first.FourMeme(ctx, usdt); err != nil || n.Found {
		t.Fatalf("FourMeme(USDT) = %+v, %v; want not found", n, err)
	}
	if m, err := first.ERC20(ctx, usdt); err != nil || m.Decimals != 18 || m.Symbol != "USDT" {
		t.Fatalf("ERC20(USDT) = %+v, %v", m, err)
	}
	before := calls.Load() // 2 getTokenInfo + decimals + symbol
	// A new process answers from the database, negative answers included.
	second := chaininfo.NewCached("bsc", st, countingReplay(t, &calls))
	b, _ := second.FourMeme(ctx, four)
	n, _ := second.FourMeme(ctx, usdt)
	m, _ := second.ERC20(ctx, usdt)
	if calls.Load() != before || b != a || n.Found || m.Symbol != "USDT" {
		t.Fatalf("second process made %d calls (want 0) and got %+v %+v %+v", calls.Load()-before, b, n, m)
	}
	if before != 4 {
		t.Errorf("first process made %d calls; want 4", before)
	}
}
