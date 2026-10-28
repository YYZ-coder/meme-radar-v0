package pgapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/contracttest"
)

// The fixed ledger models the production-sized history at the agreed
// PostgreSQL persistence seam. Expected progress is specified by the fixture,
// not derived by the health implementation. The 200ms request budget is the
// user's acceptance target; setup and schema validation are outside it.
func TestPgAdminHealthWithinBudgetWithLargeLedger(t *testing.T) {
	f := newFixture(t)
	seedLargeHealthLedger(t, f)
	h := f.handler()
	requestCtx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil).WithContext(requestCtx)
	req.Header.Set("Authorization", "Bearer test-admin")
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("large-ledger health status=%d after %s; want 200 within 200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("large-ledger health took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/admin/health", rec)
	var got api.AdminHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Parsers) != 4 {
		t.Fatalf("parser rows=%d; want four enabled modules", len(got.Parsers))
	}
	for _, p := range got.Parsers {
		wantPosition, wantLag := int64(150000), int64(0)
		switch p.Chain {
		case api.BSC:
			wantPosition, wantLag = 900000, 50000
		case api.Solana:
			wantLag = 50000
		}
		if p.Position == nil || *p.Position != wantPosition || p.LagBlocks == nil || *p.LagBlocks != wantLag || p.Failures != 0 || p.Quarantined != 0 {
			t.Errorf("%s/%s: got position=%v lag=%v failures=%d quarantined=%d; want position=%d lag=%d, no failures", p.Chain, p.Module, p.Position, p.LagBlocks, p.Failures, p.Quarantined, wantPosition, wantLag)
		}
	}
	t.Logf("health exact progress returned in %s", elapsed)
}

// Empty symbols retain the existing same-name semantics. A large equal-symbol
// population must not make a valid detail request miss its 200ms deadline.
// Newest token 1000000 is the requested token; the next ten are independently
// specified below, so dropping the same_name result cannot make this pass.
func TestPgTokenDetailWithinBudgetWithManyEmptySymbols(t *testing.T) {
	f := newFixture(t)
	seedLargeSameNamePopulation(t, f)
	h := f.handler()
	requestCtx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens/bsc/0x00000000000000000000000000000000000f4240", nil).WithContext(requestCtx)
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("equal-symbol detail status=%d after %s; want 200 within 200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("equal-symbol detail took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/tokens/{chain}/{address}", rec)
	var got api.TokenDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"0x00000000000000000000000000000000000f423f",
		"0x00000000000000000000000000000000000f423e",
		"0x00000000000000000000000000000000000f423d",
		"0x00000000000000000000000000000000000f423c",
		"0x00000000000000000000000000000000000f423b",
		"0x00000000000000000000000000000000000f423a",
		"0x00000000000000000000000000000000000f4239",
		"0x00000000000000000000000000000000000f4238",
		"0x00000000000000000000000000000000000f4237",
		"0x00000000000000000000000000000000000f4236",
	}
	if got.Token.Symbol != "" || len(got.SameName) != len(want) {
		t.Fatalf("empty symbol/detail peers=%q/%d; want empty symbol and ten peers", got.Token.Symbol, len(got.SameName))
	}
	for i, peer := range got.SameName {
		if peer.Address != want[i] || peer.Symbol != "" {
			t.Errorf("peer[%d]=%+v; want %s with unchanged empty symbol", i, peer, want[i])
		}
	}
	t.Logf("detail and ten exact same-name peers returned in %s", elapsed)
}

// Shared fixed datasets keep request tests and before/after benchmarks identical.
func seedLargeHealthLedger(t testing.TB, f *fixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, err := f.conn.Exec(ctx, `INSERT INTO chain_blocks
 (chain,block_num,block_hash,block_time,status,ingested_at,parsed_version)
 SELECT c.chain::chain_id,n,decode('01','hex'),$1,'confirmed',$1,
 CASE WHEN n<=c.parsed THEN 1 END
 FROM (VALUES ('bsc',900000,50000),('base',150000,0),('solana',150000,50000))
 c(chain,parsed,pending) CROSS JOIN LATERAL generate_series(1,c.parsed+c.pending)n`, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(ctx, `INSERT INTO chain_blocks
 (chain,block_num,status,ingested_at) VALUES ('solana',200001,'skipped',$1)`, f.now); err != nil {
		t.Fatal(err)
	}
	for chain, modules := range map[string][]string{
		"bsc": {"four_meme", "pancake_v2"}, "base": {"clanker_v4"}, "solana": {"pump_fun"},
	} {
		if err = f.st.SaveParserStatus(ctx, chain, 1, modules, f.now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.conn.Exec(ctx, `VACUUM (ANALYZE) chain_blocks`); err != nil {
		t.Fatal(err)
	}
}

func seedLargeSameNamePopulation(t testing.TB, f *fixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := f.conn.Exec(ctx, `INSERT INTO tokens
 (chain,address,symbol,name,decimals,total_supply,first_seen_at,updated_at)
 SELECT 'bsc',decode(lpad(to_hex(n),40,'0'),'hex'),'','fixture token '||n,6,1000000,$1,$1
 FROM generate_series(1,1000000)n`, f.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `INSERT INTO token_market(chain,address,last_trade_at,market_cap_usd)
 SELECT 'bsc',decode(lpad(to_hex(n),40,'0'),'hex'),$1::timestamptz-(1000001-n)*interval '1 second',n
 FROM generate_series(1,1000000)n`, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `VACUUM (ANALYZE) tokens,token_market`); err != nil {
		t.Fatal(err)
	}
}
