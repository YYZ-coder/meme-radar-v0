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

// A delay before PostgreSQL receives a query is not the age of its data.
// The public HTTP observation time must come from the actual database snapshot,
// not from the earlier time at which a cache worker started waiting for it.
func TestPgCacheUsesActualDatabaseObservationAfterDispatchDelay(t *testing.T) {
	f := healthFixture(t)
	cfg := f.conn.Config()
	address, enable := limitedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 0, 0, false, 1100*time.Millisecond)
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
	h := httpapi.New(cached, httpapi.Options{AdminToken: "test-admin"})
	request := func(ctx context.Context) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer test-admin")
		return r
	}
	enable()
	start := time.Now()
	first := httptest.NewRecorder()
	h.ServeHTTP(first, request(t.Context()))
	elapsed := time.Since(start)
	if elapsed < time.Second {
		t.Fatalf("dispatch delay did not apply: %s", elapsed)
	}
	if first.Code != 200 {
		t.Fatalf("fresh database observation after dispatch wait=%d: %s; want200, not a fabricated data-expiry timeout", first.Code, first.Body.String())
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/admin/health", first)
	var got api.AdminHealth
	if err = json.Unmarshal(first.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 3 || got.Chains[0].Head == nil || *got.Chains[0].Head != 1000 {
		t.Fatalf("real health values changed: %+v", got)
	}
	stamp := first.Header().Get("X-Data-As-Of")
	observed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || !observed.After(start.Add(time.Second)) || time.Since(observed) > time.Second {
		t.Fatalf("observation=%q must follow real query dispatch and remain <=1s old; err=%v", stamp, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	hit := httptest.NewRecorder()
	start = time.Now()
	h.ServeHTTP(hit, request(ctx))
	if hit.Code != 200 || time.Since(start) > 200*time.Millisecond || hit.Header().Get("X-Data-As-Of") != stamp {
		t.Fatalf("fresh cached hit lost original DB timestamp: status%d time%s stamp%q", hit.Code, time.Since(start), hit.Header().Get("X-Data-As-Of"))
	}
	t.Logf("actual database timestamp survives dispatch delay of %s", elapsed)
}

// The approved age is one second, including the final permitted 50ms. Both
// HTTP boundaries still reject any response older than the unchanged cap.
func TestPgCacheServesObservationStillWithinOneSecond(t *testing.T) {
	f := healthFixture(t)
	cfg := f.conn.Config()
	address, enable := limitedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 960*time.Millisecond, 0, false)
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
	h := httpapi.New(cached, httpapi.Options{AdminToken: "test-admin"})
	request := func(ctx context.Context) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer test-admin")
		return r
	}
	enable()
	dbStart := databaseClock(t, f)
	start := time.Now()
	first := httptest.NewRecorder()
	h.ServeHTTP(first, request(t.Context()))
	elapsed := time.Since(start)
	dbEnd := databaseClock(t, f)
	if elapsed < 950*time.Millisecond {
		t.Fatalf("response delay did not apply: %s", elapsed)
	}
	if first.Code != 200 {
		t.Fatalf("still-permitted database observation after response wait=%d: %s; want200, not a fabricated data-expiry timeout", first.Code, first.Body.String())
	}
	var got api.AdminHealth
	if err = json.Unmarshal(first.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 3 || got.Chains[0].Head == nil || *got.Chains[0].Head != 1000 {
		t.Fatalf("real health values changed: %+v", got)
	}
	stamp := first.Header().Get("X-Data-As-Of")
	observed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || observed.Before(dbStart) || observed.After(dbEnd) || time.Since(observed) > time.Second {
		t.Fatalf("observation=%q must retain actual query time and remain <=1s old; err=%v", stamp, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	hit := httptest.NewRecorder()
	start = time.Now()
	h.ServeHTTP(hit, request(ctx))
	if hit.Code != 200 || time.Since(start) > 200*time.Millisecond || hit.Header().Get("X-Data-As-Of") != stamp {
		t.Fatalf("fresh cached hit lost original DB timestamp: status%d time%s stamp%q", hit.Code, time.Since(start), hit.Header().Get("X-Data-As-Of"))
	}
	// Schema validation must not consume the measured observation age before the follow-up request.
	contracttest.Check(t, http.MethodGet, "/api/v1/admin/health", first)
	t.Logf("actual database timestamp survives response delay of %s", elapsed)
}

// databaseClock brackets a server-sourced timestamp with the same independent
// PostgreSQL clock. Host and database clocks need not be identical to the ms.
func databaseClock(t *testing.T, f *fixture) time.Time {
	t.Helper()
	var at time.Time
	if err := f.conn.QueryRow(t.Context(), "SELECT clock_timestamp()").Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}
