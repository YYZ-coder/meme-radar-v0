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

// Only one real PostgreSQL connection stalls; a second path still reaches the
// same database normally. Results must be exact live data, not a cached value.
func TestPgAdminHealthWithinBudgetWithOneStalledConnection(t *testing.T) {
	f := healthFixture(t)
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
	h := httpapi.New(pgapi.New(st, f.cfg, func() time.Time { return f.now }), httpapi.Options{Now: func() time.Time { return f.now }, AdminToken: "test-admin"})
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
	// A committed update after warmup prevents a stale warmup reply from passing.
	if _, err := f.conn.Exec(context.Background(), "UPDATE stream_cursors SET head=1001 WHERE chain='bsc' AND stream='forward'"); err != nil {
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
		t.Fatalf("single stalled connection health status=%d after %s; want 200 within 200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("live health took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/admin/health", rec)
	var got api.AdminHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 3 || got.Chains[0].Head == nil || *got.Chains[0].Head != 1001 || got.Chains[0].BacklogBlocks == nil || *got.Chains[0].BacklogBlocks != 11 {
		t.Fatalf("health=%+v; want newly committed head=1001 backlog=11", got.Chains)
	}
	t.Logf("fresh exact health despite a stalled connection returned in %s", elapsed)
}
