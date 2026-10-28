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

// Published historical versions remain for frozen query pagination. Once the
// current version is cached, generic planning must not scan that retired history
// before returning a distinct, freshly fenced query with the same exact values.
func TestPgCachedInflowWithinBudgetWithRetiredHistoryAndGenericPlan(t *testing.T) {
	checkInflowHistoryBudget(t, true)
}

// Regression supplement: the first values read also uses the pinned indexes.
func TestPgColdInflowWithinBudgetWithRetiredHistoryAndGenericPlan(t *testing.T) {
	checkInflowHistoryBudget(t, false)
}

func checkInflowHistoryBudget(t *testing.T, warmValues bool) {
	t.Helper()
	f := inflowFixture(t, true)
	_, err := f.conn.Exec(t.Context(), `UPDATE inflow_rows SET projection_version=projection_version+1000000;
 UPDATE projection_heads SET projection_version=projection_version+1000000;
 SELECT setval('projection_version_seq',1000100);
 INSERT INTO inflow_rows(projection_version,chain,"window",token_address,retired_at)
 SELECT v,'bsc','5m',decode(lpad(to_hex(n),40,'0'),'hex'),now()
 FROM generate_series(1,100)v CROSS JOIN generate_series(1,4000)n;
 ANALYZE inflow_rows; ANALYZE projection_heads; ANALYZE sm_lists;`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.conn.Config()
	address, enable := delayedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 175*time.Millisecond)
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Host = address
	q := u.Query()
	q.Set("sslmode", "disable")
	q.Set("pool_max_conns", "1")
	q.Set("plan_cache_mode", "force_generic_plan")
	// Replay the merge-join strategy seen in the PG18 live plan on the local PG17 server.
	// JIT is disabled so this measures row access, not optional compilation.
	q.Set("enable_nestloop", "off")
	q.Set("jit", "off")
	u.RawQuery = q.Encode()
	st, err := store.Open(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	h := httpapi.New(pgapi.New(st, f.cfg, func() time.Time { return f.now }), httpapi.Options{Now: func() time.Time { return f.now }})
	route := "/api/v1/smart-money/inflow"
	path := route + "?window=5m&sort=sm_trades&order=desc&currency=usd&limit=50"
	var warm api.InflowPage
	if warmValues {
		getPG(t, h, route, path, 200, &warm)
	} else if err := st.WarmAPIReads(t.Context()); err != nil {
		t.Fatal(err)
	}
	enable()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 || elapsed > 200*time.Millisecond {
		t.Fatalf("cached inflow with400k retired rows and175ms RTT=%d in%s; want200 <=200ms:%s", rec.Code, elapsed, rec.Body.String())
	}
	var got api.InflowPage
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.QueryID == warm.QueryID || got.Total != 4 || len(got.Items) != 4 || got.ListVersion != "2" || len(got.ChainAsOf) != 3 {
		t.Fatalf("fresh frozen metadata changed:%+v", got)
	}
	wantChains := []api.Chain{api.BSC, api.Base, api.BSC, api.BSC}
	wantAddresses := []string{"0x0000000000000000000000000000000000000061", "0x0000000000000000000000000000000000000064", "0x0000000000000000000000000000000000000062", "0x0000000000000000000000000000000000000063"}
	wantTrades := []int64{2, 1, 1, 0}
	for i, row := range got.Items {
		if row.Token.Chain != wantChains[i] || row.Token.Address != wantAddresses[i] || row.SMTrades == nil || *row.SMTrades != wantTrades[i] {
			t.Fatalf("row%d changed:%+v", i, row)
		}
	}
	if got.Items[0].Volume == nil || *got.Items[0].Volume != "3" {
		t.Fatalf("exact H4 dollar volume changed:%+v", got.Items[0])
	}
	t.Logf("new exact query with retained history returned in%s", elapsed)
}
