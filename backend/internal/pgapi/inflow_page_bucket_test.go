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

// First-page latency must depend on the requested50 values rather than the
// 4096 current candidates. H4 amounts and2000-row frozen-query cap stay exact;
// the125ms/512KiBps PostgreSQL path is real and has not served a response yet.
func TestPgFirstTradePageWithinBudgetWithLargeUnusedSolanaBucket(t *testing.T) {
	f := inflowFixture(t, true)
	_, err := f.conn.Exec(t.Context(), `INSERT INTO inflow_rows SELECT (jsonb_populate_record(NULL::inflow_rows,to_jsonb(r)||jsonb_build_object('token_address','\x'||lpad(to_hex(n),40,'0')))).* FROM inflow_rows r CROSS JOIN generate_series(1024,5115)n WHERE r.chain='bsc' AND r.token_address=$1`, raw(20, 0x61))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.conn.Exec(t.Context(), `INSERT INTO inflow_rows SELECT (jsonb_populate_record(NULL::inflow_rows,to_jsonb(r)||jsonb_build_object('projection_version',(SELECT projection_version FROM projection_heads WHERE name='inflow' AND chain='solana' AND "window"='5m'),'chain','solana','token_address','\x'||lpad(to_hex(n),64,'0'),'sm_trades',NULL))).* FROM inflow_rows r CROSS JOIN generate_series(1024,5119)n WHERE r.chain='base' AND r."window"='5m'`)
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
		t.Fatalf("first8192-candidate inflow at125ms/512KiBps=%d in%s; want200 <=200ms on the first query:%s", rec.Code, elapsed, rec.Body.String())
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
	t.Logf("first exact8192-candidate query returned within200ms in%s", elapsed)
}
