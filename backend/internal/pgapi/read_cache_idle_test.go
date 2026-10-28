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

// An idle service still owes the first health request a <=200ms response.
// Wait four full accepted-age intervals with no HTTP traffic, then verify a
// committed progress change and an actual observation no older than one second.
func TestPgPrimedHealthFirstRequestAfterIdleWithinBudget(t *testing.T) {
	f := healthFixture(t)
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
	if _, err = f.conn.Exec(t.Context(), "UPDATE stream_cursors SET head=1001 WHERE chain='bsc' AND stream='forward'"); err != nil {
		t.Fatal(err)
	}
	committed := time.Now()
	enable()
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	<-timer.C
	h := httpapi.New(cached, httpapi.Options{AdminToken: "test-admin"})
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer test-admin")
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)
	if rec.Code != 200 || elapsed > 200*time.Millisecond {
		t.Fatalf("idle first health=%d after%s; want200 within200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	var got api.AdminHealth
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 3 || got.Chains[0].Head == nil || *got.Chains[0].Head != 1001 {
		t.Fatalf("idle first health lost committed progress: %+v", got)
	}
	observed, err := time.Parse(time.RFC3339Nano, rec.Header().Get("X-Data-As-Of"))
	if err != nil || !observed.After(committed) || time.Since(observed) > time.Second {
		t.Fatalf("idle health observation invalid: %q err=%v", rec.Header().Get("X-Data-As-Of"), err)
	}
	t.Logf("first exact health after four idle age intervals returned in %s", elapsed)
}
