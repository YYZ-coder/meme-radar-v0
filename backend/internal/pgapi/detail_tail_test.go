package pgapi_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

// One real PostgreSQL connection stalls while another is available. The
// committed price update after warmup must appear in the successful response;
// a cached warmup result cannot pass this public HTTP behavior test.
func TestPgTokenDetailWithinBudgetWithOneStalledConnection(t *testing.T) {
	f := detailFixture(t, false)
	cfg := f.conn.Config()
	address, enable := delayedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 400*time.Millisecond, true)
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Host = address
	q := u.Query()
	q.Set("sslmode", "disable")
	q.Set("pool_max_conns", "2")
	u.RawQuery = q.Encode()
	st, err := store.Open(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	h := httpapi.New(pgapi.New(st, f.cfg, func() time.Time { return f.now }), httpapi.Options{Now: func() time.Time { return f.now }})
	request := func(ctx context.Context) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061", nil).WithContext(ctx)
	}
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, request(context.Background()))
	if warm.Code != 200 {
		t.Fatalf("warmup status=%d: %s", warm.Code, warm.Body.String())
	}
	if _, err := f.conn.Exec(context.Background(), "UPDATE token_market SET price_usd=77,market_cap_usd=77000000000 WHERE chain='bsc' AND address=$1", raw(20, 0x61)); err != nil {
		t.Fatal(err)
	}
	enable()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, request(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 {
		t.Fatalf("single stalled connection token detail status=%d after %s; want 200 within 200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("token detail took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/tokens/{chain}/{address}", rec)
	var got api.TokenDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Price == nil || *got.Price != "77" || got.MarketCap == nil || *got.MarketCap != "77000000000" {
		t.Fatalf("fresh committed price missing: %+v", got)
	}
	if got.AsOf != "2026-10-23T10:00:00.000Z" || got.Volume24h == nil || *got.Volume24h != "400" || len(got.Windows) != 4 || len(got.SameName) != 2 {
		t.Fatalf("detail must retain the H1 worked example: %+v", got)
	}
	for i, w := range got.Windows {
		volume := "400"
		if i == 0 {
			volume = "350"
		}
		if w.Volume == nil || *w.Volume != volume || w.Traders == nil || *w.Traders != 3 {
			t.Errorf("window[%d]=%+v; want volume=%s and 3 traders", i, w, volume)
		}
	}
	t.Logf("exact token detail with one stalled database connection returned in %s", elapsed)
}
