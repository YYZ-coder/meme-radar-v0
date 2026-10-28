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
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

// The user request selects all chains and the one-hour smart-trade ordering.
// Independent H4 facts give A=2 smart trades, Base D=1, B=1 and C=0.
func TestPgInflowAllChainsWithinBudgetWithDatabaseRTT(t *testing.T) {
	f := inflowFixture(t, false)
	for _, chain := range []string{"bsc", "base", "solana"} {
		f.apply(t, chain, 0, apiTime.Add(-2*time.Hour), parse.Facts{})
		if err := f.st.RefreshCoverage(t.Context(), chain); err != nil {
			t.Fatal(err)
		}
		published, err := f.st.RebuildInflow(t.Context(), chain, "1h", f.now)
		if err != nil {
			t.Fatal(err)
		}
		if !published.Rebuilt {
			t.Fatalf("one-hour fixture not published for %s: %+v", chain, published)
		}
	}
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
	h := httpapi.New(pgapi.New(st, f.cfg, func() time.Time { return f.now }), httpapi.Options{Now: func() time.Time { return f.now }})
	path := "/api/v1/smart-money/inflow?window=1h&sort=sm_trades&order=desc&currency=usd&limit=50"
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, httptest.NewRequest(http.MethodGet, path, nil))
	if warm.Code != http.StatusOK {
		t.Fatalf("warmup=%d: %s", warm.Code, warm.Body.String())
	}
	enable()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
	elapsed := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("all-chain inflow at75ms RTT=%d after %s; want200 within200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("inflow took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/smart-money/inflow", rec)
	var got api.InflowPage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 4 || len(got.Items) != 4 || got.ListVersion != "2" || len(got.ChainAsOf) != 3 || got.ChainAsOf[api.Base] != "2026-10-23T09:59:00.000Z" || got.AsOf != "2026-10-23T10:00:00.000Z" {
		t.Fatalf("one-hour all-chain snapshot=%+v; want exact H4 total4/list2/separate cutoffs", got)
	}
	wantChains := []api.Chain{api.BSC, api.Base, api.BSC, api.BSC}
	wantAddresses := []string{"0x0000000000000000000000000000000000000061", "0x0000000000000000000000000000000000000064", "0x0000000000000000000000000000000000000062", "0x0000000000000000000000000000000000000063"}
	wantTrades := []int64{2, 1, 1, 0}
	for i, row := range got.Items {
		if row.Token.Chain != wantChains[i] || row.Token.Address != wantAddresses[i] || row.SMTrades == nil || *row.SMTrades != wantTrades[i] {
			t.Errorf("row[%d]=%+v; want %s/%s smart=%d", i, row, wantChains[i], wantAddresses[i], wantTrades[i])
		}
	}
	if got.Items[0].Volume == nil || *got.Items[0].Volume != "3" || got.Items[0].NetInflow == nil || *got.Items[0].NetInflow != "3" {
		t.Fatalf("A exact dollar values lost: %+v", got.Items[0])
	}
	t.Logf("exact one-hour all-chain inflow returned in %s", elapsed)
}

// The deployed cached backend must also prepare the uncached inflow route.
// No inflow HTTP response has been read before this first request.
func TestPgCachedBackendInflowColdRequestWithinBudget(t *testing.T) {
	f := inflowFixture(t, false)
	for _, chain := range []string{"bsc", "base", "solana"} {
		f.apply(t, chain, 0, apiTime.Add(-2*time.Hour), parse.Facts{})
		if err := f.st.RefreshCoverage(t.Context(), chain); err != nil {
			t.Fatal(err)
		}
		published, err := f.st.RebuildInflow(t.Context(), chain, "1h", f.now)
		if err != nil || !published.Rebuilt {
			t.Fatalf("fixture publish %s: %+v %v", chain, published, err)
		}
	}
	cfg := f.conn.Config()
	address, enable := delayedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 125*time.Millisecond)
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
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/smart-money/inflow?window=1h&sort=sm_trades&order=desc&currency=usd&limit=50", nil).WithContext(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 {
		t.Fatalf("cold inflow at125ms RTT=%d after %s; want200 within200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("cold inflow took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/smart-money/inflow", rec)
	var got api.InflowPage
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 4 || len(got.Items) != 4 || got.Items[0].SMTrades == nil || *got.Items[0].SMTrades != 2 || got.Items[0].Volume == nil || *got.Items[0].Volume != "3" || got.ListVersion != "2" || got.AsOf != "2026-10-23T10:00:00.000Z" {
		t.Fatalf("cold inflow changed published H4 facts: %+v", got)
	}
	t.Logf("cold uncached inflow on deployed backend at125ms RTT returned in %s", elapsed)
}

func TestPgInflowAllChainsWithinBudgetWithOneStalledConnection(t *testing.T) {
	f := inflowFixture(t, false)
	for _, chain := range []string{"bsc", "base", "solana"} {
		f.apply(t, chain, 0, apiTime.Add(-2*time.Hour), parse.Facts{})
		if err := f.st.RefreshCoverage(t.Context(), chain); err != nil {
			t.Fatal(err)
		}
		published, err := f.st.RebuildInflow(t.Context(), chain, "1h", f.now)
		if err != nil {
			t.Fatal(err)
		}
		if !published.Rebuilt {
			t.Fatalf("one-hour fixture not published for %s: %+v", chain, published)
		}
	}
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
	st, err := store.Open(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	h := httpapi.New(pgapi.New(st, f.cfg, func() time.Time { return f.now }), httpapi.Options{Now: func() time.Time { return f.now }})
	path := "/api/v1/smart-money/inflow?window=1h&sort=sm_trades&order=desc&currency=usd&limit=50"
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, httptest.NewRequest(http.MethodGet, path, nil))
	if warm.Code != http.StatusOK {
		t.Fatalf("warmup=%d: %s", warm.Code, warm.Body.String())
	}
	enable()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
	elapsed := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("all-chain inflow with one stalled connection=%d after %s; want200 within200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("inflow took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/smart-money/inflow", rec)
	var got api.InflowPage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 4 || len(got.Items) != 4 || got.ListVersion != "2" || len(got.ChainAsOf) != 3 || got.ChainAsOf[api.Base] != "2026-10-23T09:59:00.000Z" || got.AsOf != "2026-10-23T10:00:00.000Z" {
		t.Fatalf("one-hour all-chain snapshot=%+v; want exact H4 total4/list2/separate cutoffs", got)
	}
	wantChains := []api.Chain{api.BSC, api.Base, api.BSC, api.BSC}
	wantAddresses := []string{"0x0000000000000000000000000000000000000061", "0x0000000000000000000000000000000000000064", "0x0000000000000000000000000000000000000062", "0x0000000000000000000000000000000000000063"}
	wantTrades := []int64{2, 1, 1, 0}
	for i, row := range got.Items {
		if row.Token.Chain != wantChains[i] || row.Token.Address != wantAddresses[i] || row.SMTrades == nil || *row.SMTrades != wantTrades[i] {
			t.Errorf("row[%d]=%+v; want %s/%s smart=%d", i, row, wantChains[i], wantAddresses[i], wantTrades[i])
		}
	}
	if got.Items[0].Volume == nil || *got.Items[0].Volume != "3" || got.Items[0].NetInflow == nil || *got.Items[0].NetInflow != "3" {
		t.Fatalf("A exact dollar values lost: %+v", got.Items[0])
	}
	t.Logf("exact inflow with one stalled connection returned in %s", elapsed)
}
