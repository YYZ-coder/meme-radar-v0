package pgapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/balances"
	"github.com/meme-radar/radar/internal/solana"
	"github.com/meme-radar/radar/internal/store"
)

func TestPgHoldersNativeBalanceSolana(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token, a, b := raw(32, 0x61), raw(32, 0x11), raw(32, 0x12)
	six := int16(6)
	f.apply(t, "solana", 1, apiTime, parse.Facts{Tokens: []parse.Token{{Address: token, Created: true, Decimals: &six, TotalSupply: "1000000000000000"}}})
	if err := f.st.ApplyBalances(ctx, []store.BalanceBlock{{Chain: "solana", Num: 1, Time: apiTime, Version: balances.Version, Deltas: []parse.Balance{{Owner: a, Token: token, Delta: "100"}, {Owner: b, Token: token, Delta: "50"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.RefreshCoverage(ctx, "solana"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.RefreshBalances(ctx, "solana", f.now, balances.Version); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	var fail atomic.Bool
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if req.Method != "getMultipleAccounts" || len(req.Params) != 2 {
			t.Errorf("RPC=%+v", req)
			w.WriteHeader(400)
			return
		}
		var owners []string
		if err := json.Unmarshal(req.Params[0], &owners); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if !reflect.DeepEqual(owners, []string{solana.EncodeBase58(a), solana.EncodeBase58(b)}) {
			t.Errorf("batch=%v", owners)
		}
		// Independent integer conversion: 1,000,000,001 lamports = 1.000000001 SOL.
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"context":{"slot":1},"value":[{"lamports":1000000001},{"lamports":0}]}}`)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(rpc.Close)
	for i := range f.cfg.Chains {
		f.cfg.Chains[i].LookupRPCURL = rpc.URL
		f.cfg.Chains[i].LookupRPS = 1000
	}
	h := f.handler()
	target := "/api/v1/tokens/solana/" + solana.EncodeBase58(token) + "/holders?limit=2"
	for request := 0; request < 3; request++ {
		if request == 2 {
			f.now = f.now.Add(time.Minute)
			fail.Store(true)
		}
		var got api.Holders
		getPG(t, h, holdersRoute, target, 200, &got)
		if len(got.Items) != 2 {
			t.Fatalf("rows=%+v", got)
		}
		for i, row := range got.Items {
			if request == 2 {
				if row.NativeBalance != nil {
					t.Errorf("failure must be NULL: %+v", row)
				}
			} else if row.NativeBalance == nil || *row.NativeBalance != []string{"1.000000001", "0"}[i] {
				t.Errorf("native=%v; want %s", row.NativeBalance, []string{"1.000000001", "0"}[i])
			}
		}
		want := int64(1)
		if request == 2 {
			want = 2
		}
		if calls.Load() != want {
			t.Errorf("batch/cache calls=%d,want %d", calls.Load(), want)
		}
	}
}
