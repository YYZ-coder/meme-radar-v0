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
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

// Regression supplement: simultaneous cold subscribers share useful work and
// a canceled HTTP caller cannot poison other requests to the same token.
func TestPgCachedTokenDetailConcurrentColdSubscribers(t *testing.T) {
	f := detailFixture(t, false)
	cfg := f.conn.Config()
	address, enable := delayedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 75*time.Millisecond)
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Host = address
	q := u.Query()
	q.Set("sslmode", "disable")
	q.Set("pool_max_conns", "1")
	u.RawQuery = q.Encode()
	st, err := store.Open(t.Context(), u.String())
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
	enable()
	const path = "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061"
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	aborted := httptest.NewRecorder()
	h.ServeHTTP(aborted, httptest.NewRequest(http.MethodGet, path, nil).WithContext(canceled))
	// Existing HTTP cancellation envelope is 500; deadline expiry is 503.
	if aborted.Code != 500 {
		t.Fatalf("canceled HTTP status=%d; want existing500 envelope", aborted.Code)
	}
	type result struct {
		status  int
		elapsed time.Duration
		body    []byte
		at      string
	}
	results := make(chan result, 16)
	start := make(chan struct{})
	for range 16 {
		go func() {
			<-start
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			rec := httptest.NewRecorder()
			began := time.Now()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
			results <- result{rec.Code, time.Since(began), append([]byte{}, rec.Body.Bytes()...), rec.Header().Get("X-Data-As-Of")}
		}()
	}
	close(start)
	for range 16 {
		r := <-results
		if r.status != 200 || r.elapsed > 200*time.Millisecond {
			t.Errorf("concurrent cold response=%d after%s: %s", r.status, r.elapsed, r.body)
			continue
		}
		var got api.TokenDetail
		if err = json.Unmarshal(r.body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Volume24h == nil || *got.Volume24h != "400" || len(got.Windows) != 4 || r.at == "" {
			t.Errorf("concurrent snapshot changed: %+v at=%q", got, r.at)
		}
	}
}
