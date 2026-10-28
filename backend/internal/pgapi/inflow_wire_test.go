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
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

// Published inflow rows are the durable H4 read boundary. A mostly flat exact
// decimal series must survive transport and snapshot pagination losslessly.
func TestPgInflowExactSeriesWithinTransferBudget(t *testing.T) {
	checkInflowTransferBudget(t, "")
}

// A first page pays only for returned payloads, while the query retains exact
// values across later pages and a real atomic publication of a new version.
func TestPgInflowFirstPageAvoidsUnrequestedPayload(t *testing.T) {
	checkInflowTransferBudget(t, "https://example.com/"+strings.Repeat("a", 4096))
}

func checkInflowTransferBudget(t *testing.T, website string) {
	t.Helper()
	f := inflowFixture(t, true)
	const exact = "0.1234567890123456789012345678901234567890123456789012345678901234"
	const native = "0.0000000000000000000000000000000000000000000000000000000000000007"
	_, err := f.conn.Exec(t.Context(), `UPDATE inflow_rows SET sparkline=array_fill($1::numeric,ARRAY[20]),sparkline_native=array_fill($2::numeric,ARRAY[20])
        WHERE chain='bsc' AND token_address=$3`, exact, native, raw(20, 0x61))
	if err != nil {
		t.Fatal(err)
	}
	if website != "" {
		_, err = f.conn.Exec(t.Context(), `UPDATE inflow_rows SET socials=jsonb_build_object('website',$1::text) WHERE chain='bsc' AND token_address=$2`, website, raw(20, 0x61))
		if err != nil {
			t.Fatal(err)
		}
	}
	// Copies differ only in identity. Each retains the independently specified
	// two smart trades and exact flat decimal series of the source fixture.
	_, err = f.conn.Exec(t.Context(), `INSERT INTO inflow_rows
        SELECT (jsonb_populate_record(NULL::inflow_rows,to_jsonb(r)||jsonb_build_object('token_address','\x'||lpad(to_hex(i),40,'0'),'symbol','WIRE-'||i))).*
        FROM inflow_rows r CROSS JOIN generate_series(1024,1535)i
        WHERE r.chain='bsc' AND r.token_address=$1`, raw(20, 0x61))
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.conn.Config()
	address, enable := limitedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 0, 3*1024*1024, false)
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
	path := "/api/v1/smart-money/inflow?window=5m&sort=sm_trades&order=desc&currency=usd&limit=50"
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, httptest.NewRequest(http.MethodGet, path, nil))
	if warm.Code != 200 {
		t.Fatalf("warmup=%d: %s", warm.Code, warm.Body.String())
	}
	enable()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 {
		t.Fatalf("full exact projection at3MiB/s=%d after %s; want200 within200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("full projection took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/smart-money/inflow", rec)
	var got api.InflowPage
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 516 || len(got.Items) != 50 || got.QueryID == "" || got.NextOffset == nil || *got.NextOffset != 50 {
		t.Fatalf("frozen projection metadata=%+v; want516/50/next50", got)
	}
	for _, row := range got.Items {
		if website != "" && (row.Socials.Website == nil || *row.Socials.Website != website) {
			t.Fatal("first-page website payload changed")
		}
		if row.SMTrades == nil || *row.SMTrades != 2 || len(row.Sparkline) != 20 {
			t.Fatalf("exact row shape lost: %+v", row)
		}
		for _, value := range row.Sparkline {
			if value == nil || *value != exact {
				t.Fatalf("decimal series changed: %+v", row.Sparkline)
			}
		}
	}
	// Advance the published data after freezing. The final page must retain
	// all original values, including beyond the first 50 returned rows.
	f.apply(t, "bsc", 4, apiTime.Add(time.Minute+31*time.Second), parse.Facts{})
	f.now = f.now.Add(time.Minute)
	if err = f.st.RefreshCoverage(t.Context(), "bsc"); err != nil {
		t.Fatal(err)
	}
	published, err := f.st.RebuildInflow(t.Context(), "bsc", "5m", f.now)
	if err != nil {
		t.Fatal(err)
	}
	if !published.Rebuilt || published.Rows != 3 {
		t.Fatalf("new publication=%+v; want a new 3-row BSC version", published)
	}
	var page api.InflowPage
	getPG(t, h, "/api/v1/smart-money/inflow", "/api/v1/smart-money/inflow?query_id="+url.QueryEscape(got.QueryID)+"&offset=500&limit=50", 200, &page)
	if page.Total != 516 || len(page.Items) != 16 {
		t.Fatalf("frozen last page=%+v; want total516/16", page)
	}
	if len(page.Items[0].Sparkline) != 20 || page.Items[0].Sparkline[0] == nil || *page.Items[0].Sparkline[0] != exact {
		t.Fatalf("frozen value changed: %+v", page.Items[0])
	}
	if website != "" && (page.Items[0].Socials.Website == nil || *page.Items[0].Socials.Website != website) {
		t.Fatal("frozen last-page website payload changed")
	}
	t.Logf("complete 516-row frozen projection at3MiB/s returned in %s", elapsed)
}

// Regression supplement: NULL runs, empty arrays, direction changes and native
// decimals retain the original array length and exact wire representation.
func TestPgInflowSeriesRemainLossless(t *testing.T) {
	exact := "0.123456789012345678901234567890123456789"
	small := "0.0000000000000000000000000000000000000007"
	one, two := "1", "2"
	cases := []struct {
		name, sql string
		want      []*string
	}{
		{"empty", "ARRAY[]::numeric[]", []*string{}},
		{"unknown", "ARRAY[NULL,NULL,NULL]::numeric[]", []*string{nil, nil, nil}},
		{"changes", "ARRAY[1.00,1,NULL,NULL,2,1]::numeric[]", []*string{&one, &one, nil, nil, &two, &one}},
		{"precise", "ARRAY[0.123456789012345678901234567890123456789,0.123456789012345678901234567890123456789,NULL]::numeric[]", []*string{&exact, &exact, nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := inflowFixture(t, true)
			_, err := f.conn.Exec(t.Context(), `UPDATE inflow_rows SET sparkline=`+tc.sql+`,sparkline_native=array_fill(0.0000000000000000000000000000000000000007::numeric,ARRAY[20]) WHERE chain='bsc' AND token_address=$1`, raw(20, 0x61))
			if err != nil {
				t.Fatal(err)
			}
			h := f.handler()
			var got api.InflowPage
			getPG(t, h, "/api/v1/smart-money/inflow", "/api/v1/smart-money/inflow?chain=bsc&window=5m", 200, &got)
			if len(got.Items) == 0 {
				t.Fatal("missing fixture A")
			}
			series := got.Items[0].Sparkline
			if len(series) != len(tc.want) {
				t.Fatalf("length=%d want%d", len(series), len(tc.want))
			}
			for i, want := range tc.want {
				if want == nil {
					if series[i] != nil {
						t.Errorf("series[%d]=%q wantNULL", i, *series[i])
					}
				} else if series[i] == nil || *series[i] != *want {
					t.Errorf("series[%d]=%v want%s", i, series[i], *want)
				}
			}
			var native api.InflowPage
			getPG(t, h, "/api/v1/smart-money/inflow", "/api/v1/smart-money/inflow?chain=bsc&window=5m&currency=native", 200, &native)
			if len(native.Items[0].Sparkline) != 20 {
				t.Fatal("native length lost")
			}
			for _, value := range native.Items[0].Sparkline {
				if value == nil || *value != small {
					t.Fatalf("native precision lost: %v", value)
				}
			}
		})
	}
}
