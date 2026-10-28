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

// Historical unpriced observations must not delay today's four windows. The
// exact current figures come from H1's writer-generated worked example; the
// bulk historical rows use the agreed PostgreSQL persistence seam and lie
// strictly outside every returned window. NULL prices remain unknown.
func TestPgTokenDetailWithinBudgetWithUnpricedHistory(t *testing.T) {
	f := detailFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if _, err := f.conn.Exec(ctx, `CREATE TABLE trades_20261021 PARTITION OF trades
 FOR VALUES FROM ('2026-10-21') TO ('2026-10-22')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `INSERT INTO trades
 (block_time,chain,block_num,tx_index,ix_index,tx_hash,pool_id,token_address,wallet,attribution,side,token_amount,quote_amount,quote_symbol,confirmed,decoder,decoder_version)
 SELECT '2026-10-21T10:00:00Z'::timestamptz,'bsc',1000000+n,0,0,decode('01','hex'),$1,$2,$3,'confirmed','buy',1000000,1000000000,'BNB',true,'test',1
 FROM generate_series(1,1000000)n`, raw(20, 0xa6), raw(20, 0x61), raw(20, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, "VACUUM ANALYZE trades_20261021"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, "ANALYZE trades"); err != nil {
		t.Fatal(err)
	}
	h := f.handler()
	requestCtx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061", nil).WithContext(requestCtx)
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)
	if rec.Code != 200 {
		t.Fatalf("unpriced-history detail status=%d after %s; want 200 within 200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("unpriced-history detail took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/tokens/{chain}/{address}", rec)
	var got api.TokenDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AsOf != "2026-10-23T10:00:00.000Z" || len(got.Windows) != 4 || got.Volume24h == nil || *got.Volume24h != "400" {
		t.Fatalf("old observations changed current detail: %+v", got)
	}
	for i, w := range got.Windows {
		volume, buys := "400", int64(3)
		if i == 0 {
			volume, buys = "350", 2
		}
		if w.Volume == nil || *w.Volume != volume || w.Buys == nil || *w.Buys != buys || w.Traders == nil || *w.Traders != 3 || w.PriceChangePct == nil || *w.PriceChangePct != "0" {
			t.Errorf("window[%d]=%+v; want volume=%s buys=%d traders=3 price change=0", i, w, volume, buys)
		}
	}
	t.Logf("exact four windows with historical NULL prices returned in %s", elapsed)
}
