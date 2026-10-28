package pgapi_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meme-radar/radar/internal/api"
)

func TestPgHoldersPrefersConfiguredPrivateRPC(t *testing.T) {
	f := holdingsFixture(t)
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x22b1c8c1227a0000"}`)
	}))
	defer primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x0"}`)
	}))
	defer public.Close()
	t.Setenv("RADAR_TEST_HOLDERS_RPC_URL", primary.URL)
	for i := range f.cfg.Chains {
		if f.cfg.Chains[i].Name == "bsc" {
			f.cfg.Chains[i].RPCURLEnv = "RADAR_TEST_HOLDERS_RPC_URL"
			f.cfg.Chains[i].LookupRPCURL = public.URL
		}
	}
	var got api.Holders
	getPG(t, f.handler(), holdersRoute, holdingsToken+"/holders?limit=2", http.StatusOK, &got)
	if len(got.Items) != 2 {
		t.Fatalf("holder count=%d; want 2", len(got.Items))
	}
	for _, row := range got.Items {
		if row.NativeBalance == nil || *row.NativeBalance != "2.5" {
			t.Fatalf("native balance=%v; want private RPC result 2.5", row.NativeBalance)
		}
	}
}

// Integration regression: failover must preserve the existing optional-balance contract.
func TestPgHoldersPublicFallbackAndUnknownOnTotalOutage(t *testing.T) {
	for _, tc := range []struct {
		name         string
		publicStatus int
		balance      *string
	}{
		{"public_recovers", 200, func() *string { v := "2.5"; return &v }()},
		{"both_unavailable", 503, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := holdingsFixture(t)
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
			defer primary.Close()
			public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.publicStatus)
				if tc.publicStatus == 200 {
					io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x22b1c8c1227a0000"}`)
				}
			}))
			defer public.Close()
			t.Setenv("RADAR_TEST_HOLDERS_FALLBACK_URL", primary.URL)
			for i := range f.cfg.Chains {
				if f.cfg.Chains[i].Name == "bsc" {
					f.cfg.Chains[i].RPCURLEnv = "RADAR_TEST_HOLDERS_FALLBACK_URL"
					f.cfg.Chains[i].LookupRPCURL = public.URL
					f.cfg.Chains[i].LookupRPS = 1000
				}
			}
			var got api.Holders
			getPG(t, f.handler(), holdersRoute, holdingsToken+"/holders?limit=2", http.StatusOK, &got)
			if len(got.Items) != 2 {
				t.Fatalf("holder count=%d; want 2", len(got.Items))
			}
			for _, row := range got.Items {
				if tc.balance == nil {
					if row.NativeBalance != nil {
						t.Fatal("total outage must preserve unknown; must not return zero")
					}
				} else if row.NativeBalance == nil || *row.NativeBalance != *tc.balance {
					t.Fatalf("native balance=%v; want public fallback 2.5", row.NativeBalance)
				}
			}
		})
	}
}
