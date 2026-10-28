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

// A new query must still check current membership/heads, but an immutable
// publication's values need not cross the network again for each new query ID.
func TestPgNewInflowQueriesReuseImmutableVersionsWithinBudget(t *testing.T) {
	f := inflowFixture(t, true)
	_, err := f.conn.Exec(t.Context(), `INSERT INTO inflow_rows
 SELECT (jsonb_populate_record(NULL::inflow_rows,to_jsonb(r)||jsonb_build_object('token_address','\x'||lpad(to_hex(i),40,'0'),'symbol','VERSION-'||i))).*
 FROM inflow_rows r CROSS JOIN generate_series(1024,1535)i
 WHERE r.chain='bsc' AND r.token_address=$1`, raw(20, 0x61))
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
	getPG(t, h, route, path, 200, &warm)
	enable()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 || elapsed > 200*time.Millisecond {
		t.Fatalf("new immutable-version query at175ms RTT=%d after%s; want200 <=200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	contracttest.Check(t, http.MethodGet, route, rec)
	var got api.InflowPage
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.QueryID == warm.QueryID || got.Total != 516 || len(got.Items) != 50 || got.ListVersion != "2" || len(got.ChainAsOf) != 3 {
		t.Fatalf("new query metadata=%+v; want a distinct frozen516-row list2 query", got)
	}
	for _, row := range got.Items {
		if row.SMTrades == nil || *row.SMTrades != 2 || row.Volume == nil || *row.Volume != "3" {
			t.Fatalf("immutable values changed: %+v", row)
		}
	}
	// A live activation must fence a fresh query immediately, even if every old
	// payload is cached. The old query keeps its own version and frozen values.
	if _, err = holderBackend(f).Exclude(t.Context(), api.BSC, "0x0000000000000000000000000000000000000011", "version cache fence"); err != nil {
		t.Fatal(err)
	}
	buildHolderMembers(t, f)
	getPG(t, h, route, path, 503, nil)
	var old api.InflowPage
	getPG(t, h, route, route+"?query_id="+url.QueryEscape(warm.QueryID)+"&offset=500&limit=50", 200, &old)
	if old.Total != 516 || old.ListVersion != "2" || len(old.Items) != 16 || old.Items[0].SMTrades == nil || *old.Items[0].SMTrades != 2 {
		t.Fatalf("old query lost frozen membership: %+v", old)
	}
	for _, chain := range []string{"bsc", "base", "solana"} {
		if _, err = f.st.RebuildInflow(t.Context(), chain, "5m", f.now); err != nil {
			t.Fatal(err)
		}
	}
	var fresh api.InflowPage
	getPG(t, h, route, path, 200, &fresh)
	if fresh.Total != 4 || fresh.ListVersion != "3" {
		t.Fatalf("fresh publication=%+v want4/list3", fresh)
	}
	for _, row := range fresh.Items {
		if row.Token.Chain == api.BSC && (row.SMTrades == nil || *row.SMTrades != 0) {
			t.Fatalf("cached member leaked into new version: %+v", row)
		}
	}
	t.Logf("fresh-head checked new query on immutable version returned in%s", elapsed)
}

// Regression supplement: version reuse must not mix USD and native payloads.
func TestPgImmutableInflowVersionsSeparateCurrency(t *testing.T) {
	f := inflowFixture(t, true)
	_, err := f.conn.Exec(t.Context(), `INSERT INTO inflow_rows
 SELECT (jsonb_populate_record(NULL::inflow_rows,to_jsonb(r)||jsonb_build_object('token_address','\x'||lpad(to_hex(i),40,'0')))).*
 FROM inflow_rows r CROSS JOIN generate_series(1024,1151)i WHERE r.chain='bsc' AND r.token_address=$1`, raw(20, 0x61))
	if err != nil {
		t.Fatal(err)
	}
	h := f.handler()
	route := "/api/v1/smart-money/inflow"
	for _, tc := range []struct{ currency, unit, price, volume string }{{"usd", "USD", "2", "3"}, {"native", "BNB", "0.02", "0.03"}, {"usd", "USD", "2", "3"}} {
		var got api.InflowPage
		getPG(t, h, route, route+"?chain=bsc&window=5m&sort=sm_trades&limit=1&currency="+tc.currency, 200, &got)
		if got.Total != 131 || len(got.Items) != 1 {
			t.Fatalf("%s page=%+v want131/1", tc.currency, got)
		}
		row := got.Items[0]
		if row.Currency != tc.unit || row.Price == nil || *row.Price != tc.price || row.Volume == nil || *row.Volume != tc.volume {
			t.Fatalf("%s reused values=%+v want%s price%s volume%s", tc.currency, row, tc.unit, tc.price, tc.volume)
		}
	}
}
