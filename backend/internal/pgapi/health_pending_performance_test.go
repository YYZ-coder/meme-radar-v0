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

// The exact pending count is independent of history size. Expected counts are
// literal fixture sizes at the agreed chain_blocks -> health HTTP boundary.
func TestPgHealthPendingLedgerWithinBudgetAt175msRTT(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	_, err := f.conn.Exec(ctx, `INSERT INTO chain_blocks(chain,block_num,block_hash,block_time,status,ingested_at)
 SELECT c::chain_id,n,decode('01','hex'),$1,'confirmed',$1
 FROM unnest(ARRAY['bsc','base','solana'])c CROSS JOIN generate_series(1,1000000)n`, f.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, chain := range []string{"bsc", "base", "solana"} {
		if err = f.st.SaveParserStatus(ctx, chain, 1, []string{"fixture"}, f.now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.conn.Exec(ctx, "VACUUM ANALYZE chain_blocks"); err != nil {
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
	h := httpapi.New(pgapi.New(st, f.cfg, func() time.Time { return f.now }), httpapi.Options{AdminToken: "test-admin"})
	request := func(ctx context.Context) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer test-admin")
		return r
	}
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, request(t.Context()))
	if warm.Code != 200 {
		t.Fatalf("warm=%d %s", warm.Code, warm.Body.String())
	}
	enable()
	budget, stop := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer stop()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, request(budget))
	elapsed := time.Since(start)
	if rec.Code != 200 || elapsed > 200*time.Millisecond {
		t.Fatalf("3m pending blocks at175ms RTT=%d after%s; want200 <=200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/admin/health", rec)
	var got api.AdminHealth
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Parsers) != 3 {
		t.Fatalf("parsers=%d want3", len(got.Parsers))
	}
	for _, p := range got.Parsers {
		if p.LagBlocks == nil || *p.LagBlocks != 1000000 || p.Position != nil {
			t.Errorf("%s exact pending=%+v; want1000000 and no parsed position", p.Chain, p)
		}
	}
	t.Logf("three exact million-block pending counts returned in%s", elapsed)
}
