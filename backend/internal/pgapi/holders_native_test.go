package pgapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
)

func TestPgHoldersNativeBalanceEVM(t *testing.T) {
	f := holdingsFixture(t)
	var calls atomic.Int64
	var fail atomic.Bool
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			http.Error(w, "temporary failure", 503)
			return
		}
		var req struct {
			Method string   `json:"method"`
			Params []string `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if req.Method != "eth_getBalance" || len(req.Params) != 2 || req.Params[1] != "latest" {
			t.Errorf("RPC=%+v", req)
			w.WriteHeader(400)
			return
		}
		value := "0x1" // one wei; no floating point conversion allowed
		if req.Params[0] == "0x00000000000000000000000000000000000000ee" {
			value = "0x22b1c8c1227a0000"
		} // 2.5e18
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": value}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(rpc.Close)
	for i := range f.cfg.Chains {
		if f.cfg.Chains[i].Name == "bsc" {
			f.cfg.Chains[i].LookupRPCURL = rpc.URL
		}
	}
	h := f.handler()
	check := func(want []*string) {
		t.Helper()
		var got api.Holders
		getPG(t, h, holdersRoute, holdingsToken+"/holders?limit=2", 200, &got)
		if len(got.Items) != 2 {
			t.Fatalf("rows=%+v", got)
		}
		for i, row := range got.Items {
			if want[i] == nil {
				if row.NativeBalance != nil {
					t.Errorf("failure value=%v, want null", *row.NativeBalance)
				}
			} else if row.NativeBalance == nil || *row.NativeBalance != *want[i] {
				t.Errorf("native row %d=%v, want %s", i, row.NativeBalance, *want[i])
			}
		}
	}
	a, b := "2.5", "0.000000000000000001"
	check([]*string{&a, &b})
	check([]*string{&a, &b})
	if n := calls.Load(); n != 2 {
		t.Errorf("page lookup/cache calls=%d, want 2", n)
	}
	// Exactly 60 seconds expires the cache, using the injected clock.
	f.now = f.now.Add(time.Minute)
	fail.Store(true)
	check([]*string{nil, nil})
	if n := calls.Load(); n != 4 {
		t.Errorf("expired lookups=%d, want 4", n)
	}
}
