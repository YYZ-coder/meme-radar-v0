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

// A routine health read crossing the spare-attempt threshold must not discard
// warmed capacity needed by a first token request. Real replies take125ms and
// reconnection takes600ms, both within the observed cloud timing range.
func TestPgColdDetailWithinBudgetAfterRoutineHealthHedge(t *testing.T) {
	f := detailFixture(t, false)
	cfg := f.conn.Config()
	address, enable := limitedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 125*time.Millisecond, 0, false, 0, 600*time.Millisecond)
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
	b, err := pgapi.NewCached(pgapi.New(st, f.cfg, func() time.Time { return f.now }), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	if err = b.PrimeHealth(t.Context()); err != nil {
		t.Fatal(err)
	}
	h := httpapi.New(b, httpapi.Options{AdminToken: "test-admin"})
	r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil)
	r.Header.Set("Authorization", "Bearer test-admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("warm health=%d", rec.Code)
	}
	at, err := time.Parse(time.RFC3339Nano, rec.Header().Get("X-Data-As-Of"))
	if err != nil {
		t.Fatal(err)
	}
	enable()
	// Cross the public1s observation boundary. No token response has been cached.
	if wait := time.Until(at.Add(1020 * time.Millisecond)); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		<-timer.C
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061", nil).WithContext(ctx)
	rec = httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)
	if rec.Code != 200 || elapsed > 200*time.Millisecond {
		t.Fatalf("first detail after routine health hedge=%d in%s; want200 <=200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	var got api.TokenDetail
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Volume24h == nil || *got.Volume24h != "400" || len(got.Windows) != 4 || len(got.SameName) != 2 {
		t.Fatalf("exact H1 data changed:%+v", got)
	}
	observed, err := time.Parse(time.RFC3339Nano, rec.Header().Get("X-Data-As-Of"))
	if err != nil || !observed.After(at) || time.Since(observed) > time.Second {
		t.Fatalf("first actual token observation invalid:%q", rec.Header().Get("X-Data-As-Of"))
	}
	t.Logf("first exact detail after routine health hedge in%s", elapsed)
}
