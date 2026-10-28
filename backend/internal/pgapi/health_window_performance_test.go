package pgapi_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

// Fixed fragmented ranges and gap counts exercise the agreed PostgreSQL seam.
// All expected positions/counts below come from this fixture, while the 200ms
// public request deadline is the user's acceptance target. Setup is untimed.
func TestPgAdminHealthWithinBudgetWithFragmentedCoverage(t *testing.T) {
	checkFragmentedHealth(t, 0)
}

func TestPgHealthGapCountsWithinBudgetAt175msRTT(t *testing.T) {
	checkFragmentedHealth(t, 175*time.Millisecond)
}

func checkFragmentedHealth(t *testing.T, delay time.Duration) {
	t.Helper()
	f := newFixture(t)
	f.cfg.Chains = []config.Chain{}
	for _, chain := range []string{"bsc", "base", "solana"} {
		f.cfg.Chains = append(f.cfg.Chains, config.Chain{Name: chain,
			Backfill: config.Backfill{Hours: 1},
			Ingest:   config.Ingest{BlockInterval: config.Duration(time.Second)}})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if _, err := f.conn.Exec(ctx, `INSERT INTO stream_cursors
 (chain,stream,anchor,position,head,last_block_time,updated_at)
 SELECT c::chain_id,'forward',1,1000000,1000000,$1::timestamptz-interval '7 seconds',$1
 FROM unnest(ARRAY['bsc','base','solana']) c`, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `INSERT INTO coverage_ranges(chain,from_block,to_block)
 SELECT c::chain_id,2*n,2*n FROM unnest(ARRAY['bsc','base','solana']) c,
 generate_series(1,500000) n`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `INSERT INTO stream_gaps
 (chain,from_pos,to_pos,reason,created_at,updated_at)
 SELECT c::chain_id,2*n-1,2*n-1,
 CASE WHEN n%2=0 THEN 'startup' ELSE 'rate_limited' END,$1,$1
 FROM unnest(ARRAY['bsc','base','solana']) c,generate_series(1,250000) n`, f.now); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"coverage_ranges", "stream_gaps"} {
		if _, err := f.conn.Exec(ctx, "VACUUM ANALYZE "+table); err != nil {
			t.Fatal(err)
		}
	}
	h := f.handler()
	if delay > 0 {
		cfg := f.conn.Config()
		address, enable := delayedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), delay)
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
		h = httpapi.New(pgapi.New(st, f.cfg, func() time.Time { return f.now }), httpapi.Options{AdminToken: "test-admin"})
		warm := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil)
		req.Header.Set("Authorization", "Bearer test-admin")
		h.ServeHTTP(warm, req)
		if warm.Code != 200 {
			t.Fatalf("warm=%d %s", warm.Code, warm.Body.String())
		}
		enable()
	}
	requestCtx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil).WithContext(requestCtx)
	req.Header.Set("Authorization", "Bearer test-admin")
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("fragmented health status=%d after %s; want 200 within 200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("fragmented health took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/admin/health", rec)
	var got api.AdminHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 3 {
		t.Fatalf("chain rows=%d; want three configured chains", len(got.Chains))
	}
	wantReasons := map[string]int64{"startup": 125000, "rate_limited": 125000}
	for _, c := range got.Chains {
		if c.Head == nil || *c.Head != 1000000 || c.ForwardPosition == nil || *c.ForwardPosition != 1000000 ||
			c.Anchor != 1 || c.LowerBound != 996400 || c.HistoryPosition == nil || *c.HistoryPosition != 996400 ||
			c.BacklogBlocks == nil || *c.BacklogBlocks != 0 || c.LagSeconds == nil || *c.LagSeconds != 7 ||
			c.GapsOpen != 250000 || !reflect.DeepEqual(c.GapsByReason, wantReasons) {
			t.Errorf("chain %s: got %+v; want head/forward=1000000 anchor=1 lower/history=996400 backlog=0 lag=7 gaps=250000 with two exact counts", c.Chain, c)
		}
	}
	t.Logf("fragmented health exact coverage and gaps returned in %s", elapsed)
}
