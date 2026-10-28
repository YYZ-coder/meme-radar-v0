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

// The user explicitly allowed <=1s observation age. The existing health HTTP
// seam now also exposes the actual observation time; cached hits cannot fake now.
func TestPgCachedHealthWithinBudgetPreservesObservationTime(t *testing.T) {
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
	h := httpapi.New(cached, httpapi.Options{Now: func() time.Time { return f.now }, AdminToken: "test-admin"})
	request := func(ctx context.Context) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer test-admin")
		return r
	}
	warm := httptest.NewRecorder()
	warmStart := databaseClock(t, f)
	h.ServeHTTP(warm, request(context.Background()))
	warmEnd := databaseClock(t, f)
	if warm.Code != 200 {
		t.Fatalf("warmup=%d: %s", warm.Code, warm.Body.String())
	}
	enable()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, request(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 {
		t.Fatalf("<=1s cached health status=%d after %s; want 200 within 200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("cached health took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/admin/health", rec)
	var got api.AdminHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 3 || got.Chains[0].Head == nil || *got.Chains[0].Head != 1000 || got.Chains[0].GapsOpen != 2 || got.AsOf != "2026-10-23T10:00:30.000Z" {
		t.Fatalf("cached health must preserve exact snapshot: %+v", got)
	}
	stamp := rec.Header().Get("X-Data-As-Of")
	observed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		t.Fatalf("actual observation time missing: %q", stamp)
	}
	if stamp != warm.Header().Get("X-Data-As-Of") || observed.Before(warmStart) || observed.After(warmEnd) || time.Since(observed) > time.Second {
		t.Fatalf("observation time %s must retain warmup snapshot in [%s,%s] and age<=1s", stamp, warmStart, warmEnd)
	}
	age, err := strconv.ParseInt(rec.Header().Get("X-Data-Age-Ms"), 10, 64)
	if err != nil || age < 0 || age > 1000 {
		t.Fatalf("invalid data age %q", rec.Header().Get("X-Data-Age-Ms"))
	}
	t.Logf("bounded-age exact health returned in %s", elapsed)
}

// The user explicitly allowed <=1s observation age. The existing health HTTP
// seam now also exposes the actual observation time; cached hits cannot fake now.
func TestPgCachedHealthRefreshesBeforeExpiry(t *testing.T) {
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
	h := httpapi.New(cached, httpapi.Options{Now: func() time.Time { return f.now }, AdminToken: "test-admin"})
	request := func(ctx context.Context) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer test-admin")
		return r
	}
	warm := httptest.NewRecorder()
	warmStart := databaseClock(t, f)
	h.ServeHTTP(warm, request(context.Background()))
	warmEnd := databaseClock(t, f)
	if warm.Code != 200 {
		t.Fatalf("warmup=%d: %s", warm.Code, warm.Body.String())
	}
	if _, err := f.conn.Exec(context.Background(), "UPDATE stream_cursors SET head=1001 WHERE chain='bsc' AND stream='forward'"); err != nil {
		t.Fatal(err)
	}
	enable()
	initialObserved, err := time.Parse(time.RFC3339Nano, warm.Header().Get("X-Data-As-Of"))
	if err != nil {
		t.Fatal(err)
	}
	// Advance past the explicit one-second freshness boundary, not an arbitrary
	// wait for the implementation. The background read has a 400ms transport.
	if wait := time.Until(initialObserved.Add(1100 * time.Millisecond)); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		<-timer.C
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, request(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 {
		t.Fatalf("<=1s cached health status=%d after %s; want 200 within 200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("cached health took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/admin/health", rec)
	var got api.AdminHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 3 || got.Chains[0].Head == nil || *got.Chains[0].Head != 1001 || got.Chains[0].GapsOpen != 2 || got.AsOf != "2026-10-23T10:00:30.000Z" {
		t.Fatalf("cached health must preserve exact snapshot: %+v", got)
	}
	stamp := rec.Header().Get("X-Data-As-Of")
	observed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		t.Fatalf("actual observation time missing: %q", stamp)
	}
	if stamp == warm.Header().Get("X-Data-As-Of") || !observed.After(warmEnd) || time.Since(observed) > time.Second {
		t.Fatalf("observation time %s must be newer than the warmup snapshot in [%s,%s] and age<=1s", stamp, warmStart, warmEnd)
	}
	age, err := strconv.ParseInt(rec.Header().Get("X-Data-Age-Ms"), 10, 64)
	if err != nil || age < 0 || age > 1000 {
		t.Fatalf("invalid data age %q", rec.Header().Get("X-Data-Age-Ms"))
	}
	t.Logf("bounded-age exact health returned in %s", elapsed)
}

// Regression supplement for the accepted maximum-age contract.
func TestPgCachedHealthNeverServesExpiredObservation(t *testing.T) {
	f := healthFixture(t)
	cfg := f.conn.Config()
	address, enable := delayedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 2*time.Second)
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
	h := httpapi.New(cached, httpapi.Options{Now: func() time.Time { return f.now }, AdminToken: "test-admin"})
	request := func(ctx context.Context) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer test-admin")
		return r
	}
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, request(context.Background()))
	if warm.Code != 200 {
		t.Fatalf("warmup=%d: %s", warm.Code, warm.Body.String())
	}
	if _, err := f.conn.Exec(context.Background(), "UPDATE stream_cursors SET head=1001 WHERE chain='bsc' AND stream='forward'"); err != nil {
		t.Fatal(err)
	}
	enable()
	initialObserved, err := time.Parse(time.RFC3339Nano, warm.Header().Get("X-Data-As-Of"))
	if err != nil {
		t.Fatal(err)
	}
	// Advance past the explicit one-second freshness boundary, not an arbitrary
	// wait for the implementation. The background read has a 400ms transport.
	if wait := time.Until(initialObserved.Add(1100 * time.Millisecond)); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		<-timer.C
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, request(ctx))
	elapsed := time.Since(start)
	if rec.Code != 503 {
		t.Fatalf("expired observation must fail without a fresh read: status=%d after %s body=%s", rec.Code, elapsed, rec.Body.String())
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/admin/health", rec)
	if rec.Header().Get("X-Data-As-Of") != "" {
		t.Fatal("expired snapshot timestamp leaked on failure")
	}
}

// Regression supplement: a private cached report never bypasses HTTP auth.
func TestPgCachedHealthPreservesAuthorization(t *testing.T) {
	f := healthFixture(t)
	cached, err := pgapi.NewCached(pgapi.New(f.st, f.cfg, func() time.Time { return f.now }), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cached.Close)
	h := httpapi.New(cached, httpapi.Options{AdminToken: "test-admin"})
	warm := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil)
	r.Header.Set("Authorization", "Bearer test-admin")
	h.ServeHTTP(warm, r)
	if warm.Code != 200 {
		t.Fatalf("warmup=%d", warm.Code)
	}
	for _, token := range []string{"", "Bearer wrong"} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil)
		r.Header.Set("Authorization", token)
		h.ServeHTTP(rec, r)
		if rec.Code != 403 || rec.Header().Get("X-Data-As-Of") != "" {
			t.Errorf("cached report leaked without auth: status=%d time=%s", rec.Code, rec.Header().Get("X-Data-As-Of"))
		}
	}
}
