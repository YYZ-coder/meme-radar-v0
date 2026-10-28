package pgapi_test

import (
	"context"
	"encoding/json"
	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// Two warmed database sessions suffice for these two small read reports.
// Ordinary 75ms RTT should not cause repeated 600ms connection setup stalls
// during sustained HTTP reads. Only real PG protocol responses are delayed.
func TestPgCachedReadsStayWithinBudgetWithExpensiveReconnects(t *testing.T) {
	f := detailFixture(t, false)
	cfg := f.conn.Config()
	address, enable := limitedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 75*time.Millisecond, 0, false, 0, 600*time.Millisecond)
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Host = address
	q := u.Query()
	q.Set("sslmode", "disable")
	q.Set("pool_max_conns", "2")
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
	if err = cached.PrimeHealth(t.Context()); err != nil {
		t.Fatal(err)
	}
	h := httpapi.New(cached, httpapi.Options{AdminToken: "test-admin"})
	targets := []string{"/api/v1/admin/health", "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061"}
	request := func(ctx context.Context, target string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer test-admin")
		return r
	}
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, request(t.Context(), targets[1]))
	if warm.Code != 200 {
		t.Fatalf("warmup=%d: %s", warm.Code, warm.Body.String())
	}
	enable()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	type result struct {
		path    string
		code    int
		elapsed time.Duration
		body    []byte
	}
	var failures int
	for round := 0; round < 24; round++ {
		<-ticker.C
		results := make(chan result, 2)
		for _, target := range targets {
			go func() {
				ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
				defer cancel()
				rec := httptest.NewRecorder()
				start := time.Now()
				h.ServeHTTP(rec, request(ctx, target))
				results <- result{target, rec.Code, time.Since(start), append([]byte{}, rec.Body.Bytes()...)}
			}()
		}
		for range 2 {
			r := <-results
			if r.code != 200 || r.elapsed > 200*time.Millisecond {
				failures++
				t.Logf("round%d %s=%d in%s: %s", round, r.path, r.code, r.elapsed, r.body)
				continue
			}
			if r.path == targets[1] {
				var got api.TokenDetail
				if err = json.Unmarshal(r.body, &got); err != nil {
					t.Fatal(err)
				}
				if got.Volume24h == nil || *got.Volume24h != "400" || len(got.Windows) != 4 {
					t.Fatalf("read contents lost: %+v", got)
				}
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d of48 steady reads missed200ms despite warmed sessions and75ms RTT", failures)
	}
}
