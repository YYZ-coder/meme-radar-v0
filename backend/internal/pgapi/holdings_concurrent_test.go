package pgapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/pgapi"
)

// Cold concurrent reads exercise both cache insertion and cache hits under -race.
func TestPgHoldersConcurrentNativeCache(t *testing.T) {
	f := holdingsFixture(t)
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x0"}`)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(rpc.Close)
	for i := range f.cfg.Chains {
		f.cfg.Chains[i].LookupRPCURL = rpc.URL
		f.cfg.Chains[i].LookupRPS = 1000
	}
	b := pgapi.New(f.st, f.cfg, func() time.Time { return f.now })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			<-start
			got, err := b.Holders(ctx, api.BSC, "0x0000000000000000000000000000000000000061", "all", "", 4)
			if err != nil {
				t.Error(err)
				return
			}
			if len(got.Items) != 4 {
				t.Errorf("items=%d", len(got.Items))
				return
			}
			for _, row := range got.Items {
				if row.NativeBalance == nil || *row.NativeBalance != "0" {
					t.Errorf("known zero native balance=%v", row.NativeBalance)
				}
			}
		})
	}
	close(start)
	wg.Wait()
}
