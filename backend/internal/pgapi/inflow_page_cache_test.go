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

// A new query must check current heads and membership but may reuse an
// unchanged immutable page. Chart values .001 through .02 are independent
// expectations, alongside H4 amounts and a distinct query identity.
func TestPgNewTradePageQueryReusesValuesAfterFreshHeadCheck(t *testing.T) {
	f := inflowFixture(t, true)
	_, err := f.conn.Exec(t.Context(), `INSERT INTO inflow_rows SELECT (jsonb_populate_record(NULL::inflow_rows,to_jsonb(r)||jsonb_build_object('token_address','\x'||lpad(to_hex(n),40,'0')))).* FROM inflow_rows r CROSS JOIN generate_series(1024,5115)n WHERE r.chain='bsc' AND r.token_address=$1`, raw(20, 0x61))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(t.Context(), `UPDATE inflow_rows SET sparkline=ARRAY(SELECT n::numeric/1000 FROM generate_series(1,20)n)`); err != nil {
		t.Fatal(err)
	}
	cfg := f.conn.Config()
	address, enable := limitedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 125*time.Millisecond, 128<<10, false)
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
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, httptest.NewRequest(http.MethodGet, "/api/v1/smart-money/inflow?window=5m&sort=sm_trades&order=desc&currency=usd&limit=50", nil))
	if warm.Code != 200 {
		t.Fatalf("initial page=%d:%s", warm.Code, warm.Body.String())
	}
	var previous api.InflowPage
	if err = json.Unmarshal(warm.Body.Bytes(), &previous); err != nil {
		t.Fatal(err)
	}
	enable()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/smart-money/inflow?window=5m&sort=sm_trades&order=desc&currency=usd&limit=50", nil).WithContext(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 || elapsed > 200*time.Millisecond {
		t.Fatalf("new immutable-page query at125ms/128KiBps=%d in%s; want200 <=200ms after checking current heads:%s", rec.Code, elapsed, rec.Body.String())
	}
	var got api.InflowPage
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.QueryID == previous.QueryID {
		t.Fatal("new request reused query identity without fresh fencing")
	}
	if got.Total != 2000 || !got.Truncated || len(got.Items) != 50 || got.NextOffset == nil || *got.NextOffset != 50 || got.ListVersion != "2" || len(got.ChainAsOf) != 3 {
		t.Fatalf("exact frozen metadata changed:%+v", got)
	}
	for _, row := range got.Items {
		if row.Token.Chain != api.BSC || row.SMTrades == nil || *row.SMTrades != 2 || row.Volume == nil || *row.Volume != "3" || len(row.Sparkline) != 20 || row.Sparkline[0] == nil || *row.Sparkline[0] != "0.001" || row.Sparkline[19] == nil || *row.Sparkline[19] != "0.02" {
			t.Fatalf("exact H4 page changed:%+v", row)
		}
	}
	t.Logf("fresh-head-checked exact page query returned within200ms in%s", elapsed)
}

// Regression supplement: concurrent page reuse preserves currency and creates
// independent query identities. Every request still uses the real database.
func TestPgTradePageReuseConcurrentCurrencies(t *testing.T) {
	f := inflowFixture(t, true)
	h := f.handler()
	route := "/api/v1/smart-money/inflow"
	for _, currency := range []string{"usd", "native"} {
		getPG(t, h, route, route+"?window=5m&limit=1&currency="+currency, 200, nil)
	}
	type response struct {
		currency string
		rec      *httptest.ResponseRecorder
	}
	results := make(chan response, 16)
	for i := 0; i < 16; i++ {
		currency := []string{"usd", "native"}[i%2]
		go func() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route+"?window=5m&limit=1&currency="+currency, nil))
			results <- response{currency, rec}
		}()
	}
	ids := map[string]bool{}
	for i := 0; i < 16; i++ {
		v := <-results
		if v.rec.Code != 200 {
			t.Fatalf("concurrentpage=%d:%s", v.rec.Code, v.rec.Body.String())
		}
		var got api.InflowPage
		if err := json.Unmarshal(v.rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if ids[got.QueryID] || got.ListVersion != "2" || got.Total != 4 || len(got.Items) != 1 {
			t.Fatalf("concurrent identity/metadata:%+v", got)
		}
		ids[got.QueryID] = true
		row := got.Items[0]
		volume, price, unit := "3", "2", "USD"
		if v.currency == "native" {
			volume, price, unit = "0.03", "0.02", "BNB"
		}
		if row.Currency != unit || row.Volume == nil || *row.Volume != volume || row.Price == nil || *row.Price != price || row.SMTrades == nil || *row.SMTrades != 2 {
			t.Fatalf("%s values mixed:%+v", v.currency, row)
		}
	}
}
