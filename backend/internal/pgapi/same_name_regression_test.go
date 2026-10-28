package pgapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
)

// Regression supplement for frozen semantics, not an additional claimed TDD
// Red: explicit peers are checked through the public HTTP response before and
// after real PostgreSQL transactions, including rollback and market deletion.
func TestPgSameNameFreshRankingAcrossTransactions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.conn.Exec(ctx, `INSERT INTO tokens
 (chain,address,symbol,name,decimals,total_supply,first_seen_at,updated_at) VALUES
 ('bsc',decode(lpad('01',40,'0'),'hex'),'MiXeD','target',6,1000000,$1,$1),
 ('base',decode(lpad('01',40,'0'),'hex'),'mixed','same address, other chain',6,1000000,$1,$1),
 ('bsc',decode(lpad('02',40,'0'),'hex'),'MIXED','bsc tie',6,1000000,$1,$1),
 ('base',decode(lpad('03',40,'0'),'hex'),'mixed','base tie',6,1000000,$1,$1),
 ('solana',decode(repeat('00',31)||'04','hex'),'Mixed','sol peer',6,1000000,$1,$1),
 ('bsc',decode(lpad('05',40,'0'),'hex'),'mixed','no market',6,1000000,$1,$1),
 ('bsc',decode(lpad('06',40,'0'),'hex'),'mixed','null time',6,1000000,$1,$1),
 ('base',decode(lpad('07',40,'0'),'hex'),'mixed','null time base',6,1000000,$1,$1),
 ('bsc',decode(lpad('ff',40,'0'),'hex'),'OTHER','unrelated',6,1000000,$1,$1)`, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `INSERT INTO token_market(chain,address,last_trade_at,market_cap_usd) VALUES
 ('bsc',decode(lpad('01',40,'0'),'hex'),$1::timestamptz+interval '1 minute',1),
 ('base',decode(lpad('01',40,'0'),'hex'),$1,1.5),
 ('bsc',decode(lpad('02',40,'0'),'hex'),$1::timestamptz-interval '3 minutes',2.5),
 ('base',decode(lpad('03',40,'0'),'hex'),$1::timestamptz-interval '3 minutes',3.5),
 ('solana',decode(repeat('00',31)||'04','hex'),$1::timestamptz-interval '4 minutes',4.5),
 ('bsc',decode(lpad('06',40,'0'),'hex'),NULL,6.5),
 ('base',decode(lpad('07',40,'0'),'hex'),NULL,7.5),
 ('bsc',decode(lpad('ff',40,'0'),'hex'),$1::timestamptz+interval '2 minutes',99),
 ('bsc',decode(lpad('aa',40,'0'),'hex'),$1::timestamptz+interval '3 minutes',999)`, f.now); err != nil {
		t.Fatal(err)
	}
	h := f.handler()
	const b1 = "base/0x0000000000000000000000000000000000000001"
	const b3 = "base/0x0000000000000000000000000000000000000003"
	const c2 = "bsc/0x0000000000000000000000000000000000000002"
	const s4 = "solana/11111111111111111111111111111115"
	const b7 = "base/0x0000000000000000000000000000000000000007"
	const c5 = "bsc/0x0000000000000000000000000000000000000005"
	const c6 = "bsc/0x0000000000000000000000000000000000000006"
	check := func(want []string) api.TokenDetail {
		t.Helper()
		var got api.TokenDetail
		getPG(t, h, "/api/v1/tokens/{chain}/{address}", "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000001", http.StatusOK, &got)
		if len(got.SameName) != len(want) {
			t.Fatalf("peers=%+v; want %v", got.SameName, want)
		}
		for i, p := range got.SameName {
			if key := string(p.Chain) + "/" + p.Address; key != want[i] {
				t.Errorf("peer[%d]=%s; want %s", i, key, want[i])
			}
		}
		return got
	}
	original := []string{b1, b3, c2, s4, b7, c5, c6}
	check(original)
	tx, err := f.conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE tokens SET symbol='OTHER' WHERE chain='bsc' AND address=decode(lpad('02',40,'0'),'hex')`); err != nil {
		t.Fatal(err)
	}
	check(original) // Uncommitted metadata is not visible through another connection.
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	check(original)
	if _, err = f.conn.Exec(ctx, `UPDATE tokens SET symbol='OTHER' WHERE chain='bsc' AND address=decode(lpad('02',40,'0'),'hex')`); err != nil {
		t.Fatal(err)
	}
	check([]string{b1, b3, s4, b7, c5, c6})
	if _, err = f.conn.Exec(ctx, `UPDATE token_market SET last_trade_at=$1,market_cap_usd=35 WHERE chain='base' AND address=decode(lpad('03',40,'0'),'hex')`, f.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := check([]string{b3, b1, s4, b7, c5, c6})
	if got.SameName[0].MarketCapUSD == nil || *got.SameName[0].MarketCapUSD != "35" {
		t.Fatalf("updated market cap=%v; want 35", got.SameName[0].MarketCapUSD)
	}
	if _, err = f.conn.Exec(ctx, `DELETE FROM token_market WHERE chain='base' AND address=decode(lpad('03',40,'0'),'hex')`); err != nil {
		t.Fatal(err)
	}
	check([]string{b1, s4, b3, b7, c5, c6})
	if _, err = f.conn.Exec(ctx, `DELETE FROM tokens WHERE chain='base' AND address=decode(lpad('01',40,'0'),'hex')`); err != nil {
		t.Fatal(err)
	}
	check([]string{s4, b3, b7, c5, c6})
	if _, err = f.conn.Exec(ctx, `UPDATE tokens SET symbol='UNIQUE' WHERE chain='bsc' AND address=decode(lpad('01',40,'0'),'hex')`); err != nil {
		t.Fatal(err)
	}
	check([]string{})
}

// Regression supplement: alternating wide and rare groups through one backend
// must remain responsive after repeated executions and preserve exact peers.
func TestPgSameNameResponsiveAcrossMixedSelectivity(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.conn.Exec(ctx, `INSERT INTO tokens
 (chain,address,symbol,name,decimals,total_supply,first_seen_at,updated_at)
 SELECT 'bsc',decode(lpad(to_hex(n),40,'0'),'hex'),CASE WHEN n<=2 THEN 'RaRe' ELSE 'bulk' END,
 'selectivity fixture',6,1000000,$1,$1 FROM generate_series(1,200000)n`, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `INSERT INTO token_market(chain,address,last_trade_at)
 SELECT 'bsc',decode(lpad(to_hex(n),40,'0'),'hex'),$1::timestamptz-(200001-n)*interval '1 second'
 FROM generate_series(1,200000)n`, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `VACUUM (ANALYZE) tokens,token_market`); err != nil {
		t.Fatal(err)
	}
	h := f.handler()
	for round := 0; round < 12; round++ {
		for _, tc := range []struct {
			target, first string
			count         int
		}{
			{"0x0000000000000000000000000000000000030d40", "0x0000000000000000000000000000000000030d3f", 10},
			{"0x0000000000000000000000000000000000000002", "0x0000000000000000000000000000000000000001", 1},
		} {
			requestCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens/bsc/"+tc.target, nil).WithContext(requestCtx)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			cancel()
			if rec.Code != http.StatusOK {
				t.Fatalf("round %d target %s: status=%d; want 200 within 200ms: %s", round, tc.target, rec.Code, rec.Body.String())
			}
			var got api.TokenDetail
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.SameName) != tc.count || got.SameName[0].Address != tc.first {
				t.Fatalf("round %d target %s: peers=%+v; want %d with first=%s", round, tc.target, got.SameName, tc.count, tc.first)
			}
		}
	}
}
