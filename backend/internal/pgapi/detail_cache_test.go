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

// The user accepted at most one second of observation age for token detail.
// Preserve all four H1 windows and the original observation time on a hit.
func TestPgCachedTokenDetailWithinBudgetPreservesObservationTime(t *testing.T) {
	f := detailFixture(t, false)
	cfg := f.conn.Config()
	address, enable := delayedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 400*time.Millisecond)
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Host = address
	q := u.Query()
	q.Set("sslmode", "disable")
	q.Set("pool_max_conns", "1")
	u.RawQuery = q.Encode()
	st, err := store.Open(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	cached, err := pgapi.NewCached(pgapi.New(st, f.cfg, func() time.Time { return f.now }), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cached.Close)
	h := httpapi.New(cached, httpapi.Options{Now: func() time.Time { return f.now }})
	request := func(ctx context.Context) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061", nil).WithContext(ctx)
	}
	warm := httptest.NewRecorder()
	warmStart := databaseClock(t, f)
	h.ServeHTTP(warm, request(context.Background()))
	warmEnd := databaseClock(t, f)
	if warm.Code != 200 {
		t.Fatalf("warmup status=%d: %s", warm.Code, warm.Body.String())
	}
	enable()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, request(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 {
		t.Fatalf("<=1s cached token detail status=%d after %s; want 200 within 200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("token detail took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/tokens/{chain}/{address}", rec)
	var got api.TokenDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
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
	stamp := rec.Header().Get("X-Data-As-Of")
	observed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || stamp != warm.Header().Get("X-Data-As-Of") || observed.Before(warmStart) || observed.After(warmEnd) || time.Since(observed) > time.Second {
		t.Fatalf("actual cached detail time invalid: %q", stamp)
	}
	t.Logf("bounded-age exact token detail returned in %s", elapsed)
}

// Regression supplement: currency and token identity cannot share a snapshot.
func TestPgCachedTokenDetailSeparatesKeys(t *testing.T) {
	f := detailFixture(t, false)
	cached, err := pgapi.NewCached(pgapi.New(f.st, f.cfg, func() time.Time { return f.now }), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cached.Close)
	h := httpapi.New(cached, httpapi.Options{})
	for _, tc := range []struct{ path, currency, volume, name string }{
		{"/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061", "USD", "400", "Demo token"},
		{"/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061?currency=native", "BNB", "4", "Demo token"},
		{"/api/v1/tokens/bsc/0x0000000000000000000000000000000000000062", "USD", "100", "Older"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s=%d %s", tc.path, rec.Code, rec.Body.String())
		}
		var got api.TokenDetail
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Currency != tc.currency || got.Token.Name != tc.name || got.Volume24h == nil || *got.Volume24h != tc.volume {
			t.Errorf("key isolation for %s: got %+v; want %s/%s/%s", tc.path, got, tc.currency, tc.name, tc.volume)
		}
	}
}
