package pgapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

// Slow inflow reads must not consume the connections needed for the first
// health/detail observation. PostgreSQL remains available: only inflow_rows is
// locked, modelling a slow bulk read while the two lightweight routes are cold.
func TestPgColdHealthAndDetailRemainAvailableDuringBlockedInflow(t *testing.T) {
	f := detailFixture(t, false)
	u, err := url.Parse(f.conn.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
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
	h := httpapi.New(b, httpapi.Options{AdminToken: "test-admin"})
	tx, err := f.conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `LOCK TABLE inflow_rows IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 2)
	defer func() {
		_ = tx.Rollback(context.Background())
		for range 2 {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("bulk reader did not finish after unlock")
			}
		}
	}()
	for range 2 {
		go func() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/smart-money/inflow?window=1h", nil).WithContext(t.Context()))
			done <- struct{}{}
		}()
	}
	// Synchronize on actual PostgreSQL lock waiters, not a guessed sleep. These
	// two independent public inflow requests must be in flight before timing.
	deadline := time.Now().Add(time.Second)
	for {
		var n int
		if err = tx.QueryRow(t.Context(), `SELECT count(*) FROM pg_locks WHERE database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND relation='inflow_rows'::regclass AND NOT granted`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bulk requests did not reach the controlled table lock")
		}
		time.Sleep(2 * time.Millisecond)
	}
	for _, tc := range []struct{ path, route string }{{"/api/v1/admin/health", "/api/v1/admin/health"}, {"/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061", "/api/v1/tokens/{chain}/{address}"}} {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		r := httptest.NewRequest(http.MethodGet, tc.path, nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer test-admin")
		rec := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(rec, r)
		elapsed := time.Since(start)
		cancel()
		if rec.Code != 200 || elapsed > 200*time.Millisecond {
			t.Fatalf("cold %s during blocked inflow=%d after%s; want200 <=200ms: %s", tc.path, rec.Code, elapsed, rec.Body.String())
		}
		contracttest.Check(t, http.MethodGet, tc.route, rec)
		if rec.Header().Get("X-Data-As-Of") == "" || rec.Header().Get("X-Radar-Mock") != "" {
			t.Fatal("real observation metadata missing")
		}
		if tc.route == "/api/v1/admin/health" {
			var got api.AdminHealth
			if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Chains) != 3 {
				t.Fatal("missing real chain reports")
			}
		} else {
			var got api.TokenDetail
			if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Volume24h == nil || *got.Volume24h != "400" || len(got.Windows) != 4 || len(got.SameName) != 2 {
				t.Fatalf("H1 values changed: %+v", got)
			}
		}
		t.Logf("cold %s remains available in%s while bulk reads wait", tc.path, elapsed)
	}
}
