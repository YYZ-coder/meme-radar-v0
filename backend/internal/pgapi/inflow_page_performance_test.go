package pgapi_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

// First-page latency must depend on the requested50 values rather than the
// 4096 current candidates. H4 amounts and2000-row frozen-query cap stay exact;
// the125ms/512KiBps PostgreSQL path is real and has not served a response yet.
func TestPgColdInflowTradePageWithinBudgetWithDensePublication(t *testing.T) {
	f := inflowFixture(t, true)
	_, err := f.conn.Exec(t.Context(), `INSERT INTO inflow_rows SELECT (jsonb_populate_record(NULL::inflow_rows,to_jsonb(r)||jsonb_build_object('token_address','\x'||lpad(to_hex(n),40,'0')))).* FROM inflow_rows r CROSS JOIN generate_series(1024,5115)n WHERE r.chain='bsc' AND r.token_address=$1`, raw(20, 0x61))
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.conn.Config()
	address, enable := limitedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 125*time.Millisecond, 512<<10, false)
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
	if err = st.WarmAPIReads(t.Context()); err != nil {
		t.Fatal(err)
	}
	h := httpapi.New(pgapi.New(st, f.cfg, func() time.Time { return f.now }), httpapi.Options{Now: func() time.Time { return f.now }})
	enable()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/smart-money/inflow?window=5m&sort=sm_trades&order=desc&currency=usd&limit=50", nil).WithContext(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 || elapsed > 200*time.Millisecond {
		t.Fatalf("first4096-candidate inflow at125ms/512KiBps=%d in%s; want200 <=200ms on the first query:%s", rec.Code, elapsed, rec.Body.String())
	}
	var got api.InflowPage
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 2000 || !got.Truncated || len(got.Items) != 50 || got.NextOffset == nil || *got.NextOffset != 50 || got.ListVersion != "2" || len(got.ChainAsOf) != 3 {
		t.Fatalf("exact frozen metadata changed:%+v", got)
	}
	for _, row := range got.Items {
		if row.Token.Chain != api.BSC || row.SMTrades == nil || *row.SMTrades != 2 || row.Volume == nil || *row.Volume != "3" {
			t.Fatalf("exact H4 page changed:%+v", row)
		}
	}
	t.Logf("first exact4096-candidate query returned within200ms in%s", elapsed)
}

// Regression supplement: score ties use float64 semantics and native address
// text. 58 encodes as21 and57 asz, so Solana text order reverses byte order.
func TestPgTradePagePreservesFloatTiesAndSolanaTextOrder(t *testing.T) {
	f := inflowFixture(t, true)
	ctx := t.Context()
	if _, err := f.conn.Exec(ctx, `UPDATE inflow_rows SET sm_trades=CASE WHEN chain='base' THEN 0 WHEN token_address=$1 THEN 9007199254740992 WHEN token_address=$2 THEN 9007199254740993 END`, raw(20, 0x61), raw(20, 0x62)); err != nil {
		t.Fatal(err)
	}
	for _, tail := range []byte{57, 58} {
		_, err := f.conn.Exec(ctx, `INSERT INTO inflow_rows SELECT (jsonb_populate_record(NULL::inflow_rows,to_jsonb(r)||jsonb_build_object('projection_version',(SELECT projection_version FROM projection_heads WHERE name='inflow' AND chain='solana' AND "window"='5m'),'chain','solana','token_address',$1::text,'sm_trades',NULL))).* FROM inflow_rows r WHERE chain='base' AND "window"='5m'`, `\x`+strings.Repeat("00", 31)+map[byte]string{57: "39", 58: "3a"}[tail])
		if err != nil {
			t.Fatal(err)
		}
	}
	h := f.handler()
	route := "/api/v1/smart-money/inflow"
	var first api.InflowPage
	getPG(t, h, route, route+"?window=5m&limit=2", 200, &first)
	if first.Total != 6 || len(first.Items) != 2 || first.Items[0].Token.Address != "0x0000000000000000000000000000000000000061" || first.Items[1].Token.Address != "0x0000000000000000000000000000000000000062" || first.Items[0].SMTrades == nil || *first.Items[0].SMTrades != 9007199254740992 || first.Items[1].SMTrades == nil || *first.Items[1].SMTrades != 9007199254740993 {
		t.Fatalf("float64 tie or exact values changed:%+v", first)
	}
	var tailPage api.InflowPage
	getPG(t, h, route, route+"?query_id="+first.QueryID+"&offset=2&limit=100", 200, &tailPage)
	want := []string{"0x0000000000000000000000000000000000000064", "0x0000000000000000000000000000000000000063", strings.Repeat("1", 31) + "21", strings.Repeat("1", 31) + "z"}
	if len(tailPage.Items) != 4 || tailPage.NextOffset != nil {
		t.Fatalf("tail metadata:%+v", tailPage)
	}
	for i, v := range tailPage.Items {
		if v.Token.Address != want[i] {
			t.Fatalf("row%d=%s want%s", i, v.Token.Address, want[i])
		}
		if i > 0 && v.SMTrades != nil {
			t.Fatalf("unknown smart count changed:%+v", v)
		}
	}
	// Mark the agreed rebuild flag so the real publisher replaces the heads
	// even though the fixture cutoff and membership have not advanced.
	if _, err := f.conn.Exec(t.Context(), `UPDATE projection_heads SET rebuild_pending=true WHERE name='inflow' AND "window"='5m'`); err != nil {
		t.Fatal(err)
	}
	for _, chain := range []string{"bsc", "base", "solana"} {
		if built, err := f.st.RebuildInflow(ctx, chain, "5m", f.now); err != nil || !built.Rebuilt {
			t.Fatalf("fixture publication %s: %+v %v", chain, built, err)
		}
	}
	var old api.InflowPage
	getPG(t, h, route, route+"?query_id="+first.QueryID+"&offset=2&limit=100", 200, &old)
	a, _ := json.Marshal(tailPage)
	b, _ := json.Marshal(old)
	if string(a) != string(b) {
		t.Fatalf("frozen page changed after new publications:%s !=%s", a, b)
	}
	var fresh api.InflowPage
	getPG(t, h, route, route+"?window=5m", 200, &fresh)
	if fresh.Total != 4 || fresh.Items[0].SMTrades == nil || *fresh.Items[0].SMTrades != 2 {
		t.Fatalf("new query failed to follow current heads:%+v", fresh)
	}
}

// Regression supplement: pages near the2000-row cap remain available from
// retired publications; offsets past the cap must not reveal extra candidates.
func TestPgTradePagesFreezeTruncatedLaterPages(t *testing.T) {
	f := inflowFixture(t, true)
	if _, err := f.conn.Exec(t.Context(), `INSERT INTO inflow_rows SELECT (jsonb_populate_record(NULL::inflow_rows,to_jsonb(r)||jsonb_build_object('token_address','\x'||lpad(to_hex(n),40,'0')))).* FROM inflow_rows r CROSS JOIN generate_series(1024,5115)n WHERE r.chain='bsc' AND r.token_address=$1`, raw(20, 0x61)); err != nil {
		t.Fatal(err)
	}
	h := f.handler()
	route := "/api/v1/smart-money/inflow"
	var first api.InflowPage
	getPG(t, h, route, route+"?window=5m&limit=50", 200, &first)
	// Mark the agreed rebuild flag so the real publisher replaces the heads
	// even though the fixture cutoff and membership have not advanced.
	if _, err := f.conn.Exec(t.Context(), `UPDATE projection_heads SET rebuild_pending=true WHERE name='inflow' AND "window"='5m'`); err != nil {
		t.Fatal(err)
	}
	for _, chain := range []string{"bsc", "base", "solana"} {
		if built, err := f.st.RebuildInflow(t.Context(), chain, "5m", f.now); err != nil || !built.Rebuilt {
			t.Fatalf("fixture publication %s: %+v %v", chain, built, err)
		}
	}
	var tailPage api.InflowPage
	getPG(t, h, route, route+"?query_id="+first.QueryID+"&offset=1950&limit=100", 200, &tailPage)
	if tailPage.Total != 2000 || !tailPage.Truncated || len(tailPage.Items) != 50 || tailPage.NextOffset != nil || tailPage.ListVersion != "2" {
		t.Fatalf("truncated frozen tail:%+v", tailPage)
	}
	for _, r := range tailPage.Items {
		if r.SMTrades == nil || *r.SMTrades != 2 || r.Volume == nil || *r.Volume != "3" {
			t.Fatalf("frozen exact row changed:%+v", r)
		}
	}
	var end api.InflowPage
	getPG(t, h, route, route+"?query_id="+first.QueryID+"&offset=2000&limit=100", 200, &end)
	if len(end.Items) != 0 || end.NextOffset != nil || end.Total != 2000 {
		t.Fatalf("cap exposed more rows:%+v", end)
	}
}
