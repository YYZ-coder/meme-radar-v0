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

// A local API -> frontend -> caller handoff can take15ms (live warm C1
// measurements). A nearly expired observation must wait briefly for the already
// running refresh, not become an expired200 or an avoidable frontend503.
func TestPgCachedHealthKeepsFreshnessAcrossLocalHandoff(t *testing.T) {
	f := healthFixture(t)
	cfg := f.conn.Config()
	address, enable := delayedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 600*time.Millisecond)
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
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, request(t.Context()))
	if warm.Code != 200 {
		t.Fatalf("warm=%d %s", warm.Code, warm.Body.String())
	}
	observed, err := time.Parse(time.RFC3339Nano, warm.Header().Get("X-Data-As-Of"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(t.Context(), `UPDATE stream_cursors SET head=1001 WHERE chain='bsc' AND stream='forward'`); err != nil {
		t.Fatal(err)
	}
	enable()
	// Deliberately approach the approved age boundary. The600ms refresh begun
	// before expiry is still in flight; setup/wait are outside request timing.
	if wait := time.Until(observed.Add(990 * time.Millisecond)); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		<-timer.C
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, request(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 || elapsed > 200*time.Millisecond {
		t.Fatalf("handoff-ready health=%d after%s want200 <=200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	at, err := time.Parse(time.RFC3339Nano, rec.Header().Get("X-Data-As-Of"))
	if err != nil {
		t.Fatal(err)
	}
	ageAtCaller := time.Since(at) + 15*time.Millisecond
	if ageAtCaller > time.Second {
		t.Fatalf("successful snapshot becomes%s old after local handoff; want<=1s", ageAtCaller)
	}
	var got api.AdminHealth
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !at.After(observed) || len(got.Chains) != 3 || got.Chains[0].Head == nil || *got.Chains[0].Head != 1001 {
		t.Fatalf("expected actual refreshed head1001 with a newer snapshot, got%s %+v", at, got.Chains)
	}
	t.Logf("fresh exact health returns in%s with%s age including15ms handoff", elapsed, ageAtCaller)
}
