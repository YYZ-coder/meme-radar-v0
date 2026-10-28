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

// A first query must not hit the existing2s deadline simply because the
// candidate set grows. The live report currently transfers30 metadata columns,
// including unused placeholders and four-key JSON presence objects per token.
func TestPgColdInflowAvoidsDefaultTimeoutWithDenseSelection(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/smart-money/inflow?window=5m&sort=sm_trades&order=desc&currency=usd&limit=50", nil).WithContext(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 || elapsed > 2*time.Second {
		t.Fatalf("first4096-candidate inflow at125ms/512KiBps=%d in%s; want200 before unchanged2s deadline:%s", rec.Code, elapsed, rec.Body.String())
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
	t.Logf("first exact4096-candidate query avoided default timeout in%s", elapsed)
}

// Regression supplement: compact presence must preserve missing/null/empty/URL
// distinctions at the public filter and complete-page boundaries.
func TestPgDenseInflowSelectionPreservesSocialPresenceAndURLs(t *testing.T) {
	f := inflowFixture(t, true)
	_, err := f.conn.Exec(t.Context(), "UPDATE inflow_rows SET socials='{}'")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.conn.Exec(t.Context(), `INSERT INTO inflow_rows SELECT (jsonb_populate_record(NULL::inflow_rows,to_jsonb(r)||jsonb_build_object('token_address','\x'||lpad(to_hex(n),40,'0'),'socials',jsonb_build_object(
 'twitter',CASE WHEN (n&1)<>0 THEN CASE WHEN n%32<16 THEN '' ELSE 'https://social.example/known' END END,
 'telegram',CASE WHEN (n&2)<>0 THEN CASE WHEN n%32<16 THEN '' ELSE 'https://social.example/known' END END,
 'github',CASE WHEN (n&4)<>0 THEN CASE WHEN n%32<16 THEN '' ELSE 'https://social.example/known' END END,
 'website',CASE WHEN (n&8)<>0 THEN CASE WHEN n%32<16 THEN '' ELSE 'https://social.example/known' END END)))).*
 FROM inflow_rows r CROSS JOIN generate_series(1024,1151)n WHERE r.chain='bsc' AND r.token_address=$1`, raw(20, 0x61))
	if err != nil {
		t.Fatal(err)
	}
	h := f.handler()
	route := "/api/v1/smart-money/inflow"
	for _, tc := range []struct {
		fields string
		total  int64
	}{{"twitter", 64}, {"telegram", 64}, {"github", 64}, {"website", 64}, {"twitter,telegram,github,website", 8}} {
		var got api.InflowPage
		getPG(t, h, route, route+"?chain=bsc&window=5m&limit=100&socials="+tc.fields, 200, &got)
		if got.Total != tc.total || int64(len(got.Items)) != tc.total {
			t.Fatalf("socials%s got%+v want%d", tc.fields, got, tc.total)
		}
		empty, urlValue := false, false
		for _, row := range got.Items {
			fields := map[string]*string{"twitter": row.Socials.Twitter, "telegram": row.Socials.Telegram, "github": row.Socials.Github, "website": row.Socials.Website}
			for _, name := range strings.Split(tc.fields, ",") {
				v := fields[name]
				if v == nil {
					t.Fatalf("requested%s missing from actual payload:%+v", name, row)
				}
				switch *v {
				case "":
					empty = true
				case "https://social.example/known":
					urlValue = true
				default:
					t.Fatalf("URL altered:%q", *v)
				}
			}
		}
		if !empty || !urlValue {
			t.Fatalf("%s lost empty-string or complete URL values", tc.fields)
		}
	}
}
