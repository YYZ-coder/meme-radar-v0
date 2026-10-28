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

// The public HTTP and real PostgreSQL protocol seams are the same boundaries
// agreed for the previous optimization. Fixed 75ms transport latency models
// observed database RTT without inventing database replies or call counts.
func TestPgTokenDetailWithinBudgetWithDatabaseRTT(t *testing.T) {
	f := detailFixture(t, false)
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
	st, err := store.Open(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	h := httpapi.New(pgapi.New(st, f.cfg, func() time.Time { return f.now }), httpapi.Options{Now: func() time.Time { return f.now }})
	request := func(ctx context.Context) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061", nil).WithContext(ctx)
	}
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, request(context.Background()))
	if warm.Code != 200 {
		t.Fatalf("warmup status=%d: %s", warm.Code, warm.Body.String())
	}
	enable()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, request(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 {
		t.Fatalf("75ms RTT token detail status=%d after %s; want 200 within 200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("token detail took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/tokens/{chain}/{address}", rec)
	var got api.TokenDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AsOf != "2026-10-23T10:00:00.000Z" || got.Volume24h == nil || *got.Volume24h != "400" || len(got.Windows) != 4 || len(got.SameName) != 2 {
		t.Fatalf("detail must retain the H1 worked example: %+v", got)
	}
	for i, w := range got.Windows {
		volume := "400"
		if i == 0 {
			volume = "350"
		}
		if w.Volume == nil || *w.Volume != volume || w.Traders == nil || *w.Traders != 3 {
			t.Errorf("window[%d]=%+v; want volume=%s and 3 traders", i, w, volume)
		}
	}
	t.Logf("exact token detail with 75ms database RTT returned in %s", elapsed)
}
