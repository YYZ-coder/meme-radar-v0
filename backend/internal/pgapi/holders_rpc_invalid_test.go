package pgapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meme-radar/radar/internal/api"
)

func TestPgHoldersMalformedNativeBalanceIsNull(t *testing.T) {
	f := holdingsFixture(t)
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// An EVM quantity has hexadecimal digits after 0x, never a sign.
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x+1"}`)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(rpc.Close)
	for i := range f.cfg.Chains {
		f.cfg.Chains[i].LookupRPCURL = rpc.URL
	}
	var got api.Holders
	getPG(t, f.handler(), holdersRoute, holdingsToken+"/holders?limit=2", 200, &got)
	if len(got.Items) != 2 {
		t.Fatalf("rows=%+v", got)
	}
	for _, row := range got.Items {
		if row.NativeBalance != nil {
			t.Errorf("invalid RPC quantity became %s, want NULL", *row.NativeBalance)
		}
	}
}
