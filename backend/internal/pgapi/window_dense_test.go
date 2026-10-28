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

// The agreed PostgreSQL persistence seam adds 200,000 one-dollar buys by
// H1's existing wallet1 in the last minute. The real stats writer derives the
// amounts; distinct traders must remain3 in every overlapping window. The
// first public read has no response priming and retains the200ms budget.
func TestPgColdDetailWithinBudgetWithDenseRepeatedWallets(t *testing.T) {
	f := detailFixture(t, false)
	ctx := t.Context()
	_, err := f.conn.Exec(ctx, `INSERT INTO trades
 (block_time,chain,block_num,tx_index,ix_index,tx_hash,pool_id,token_address,wallet,attribution,side,token_amount,quote_amount,quote_symbol,value_usd,value_native,confirmed,decoder,decoder_version)
 SELECT '2026-10-23T09:59:30Z'::timestamptz,'bsc',1000000+n,0,0,decode('01','hex'),$1,$2,$3,'confirmed','buy',1000000,10000000000000000,'BNB',1,0.01,true,'test',1
 FROM generate_series(1,200000)n`, raw(20, 0xa6), raw(20, 0x61), raw(20, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(ctx, `INSERT INTO stats_dirty(chain,minute) VALUES ('bsc','2026-10-23T09:59:00Z') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err = f.st.RefreshStats(ctx, "bsc", f.now); err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(ctx, "VACUUM ANALYZE trades_20261023"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(ctx, "ANALYZE trades"); err != nil {
		t.Fatal(err)
	}
	cfg := f.conn.Config()
	address, enable := delayedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 125*time.Millisecond)
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Host = address
	q := u.Query()
	q.Set("sslmode", "disable")
	q.Set("pool_max_conns", "1")
	u.RawQuery = q.Encode()
	st, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err = st.WarmAPIReads(ctx); err != nil {
		t.Fatal(err)
	}
	h := httpapi.New(pgapi.New(st, f.cfg, func() time.Time { return f.now }), httpapi.Options{})
	enable()
	requestCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061", nil).WithContext(requestCtx))
	elapsed := time.Since(start)
	if rec.Code != 200 || elapsed > 200*time.Millisecond {
		t.Fatalf("first dense-wallet detail=%d in%s; want200 <=200ms:%s", rec.Code, elapsed, rec.Body.String())
	}
	var got api.TokenDetail
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Volume24h == nil || *got.Volume24h != "200400" || len(got.Windows) != 4 || len(got.SameName) != 2 {
		t.Fatalf("dense H1 values changed:%+v", got)
	}
	for i, w := range got.Windows {
		volume, net, buy, buys := "200400", "200300", "200350", int64(200003)
		if i == 0 {
			volume, net, buy, buys = "200350", "200250", "200300", 200002
		}
		if w.Traders == nil || *w.Traders != 3 || w.Volume == nil || *w.Volume != volume || w.NetInflow == nil || *w.NetInflow != net || w.BuyVolume == nil || *w.BuyVolume != buy || w.SellVolume == nil || *w.SellVolume != "50" || w.Buys == nil || *w.Buys != buys || w.Sells == nil || *w.Sells != 1 {
			t.Fatalf("window%d exact values changed:%+v", i, w)
		}
	}
	t.Logf("first exact200k repeated-wallet detail returned in%s", elapsed)
}

// Regression supplement: the single scan must preserve inclusive lower and
// exclusive upper boundaries, NULL-wallet exclusion, and cross-window reuse.
func TestPgTokenWindowDistinctWalletsRespectBoundaries(t *testing.T) {
	f := detailFixture(t, false)
	ctx := t.Context()
	if _, err := f.conn.Exec(ctx, `CREATE TABLE trades_20261022 PARTITION OF trades FOR VALUES FROM ('2026-10-22') TO ('2026-10-23')`); err != nil {
		t.Fatal(err)
	}
	_, err := f.conn.Exec(ctx, `INSERT INTO trades
 (block_time,chain,block_num,tx_index,ix_index,tx_hash,pool_id,token_address,wallet,attribution,side,token_amount,quote_amount,quote_symbol,value_usd,value_native,confirmed,decoder,decoder_version)
 SELECT bt,'bsc',1000000+n,0,0,decode('01','hex'),$1,$2,wallet,'confirmed','buy',1000000,10000000000000000,'BNB',1,0.01,true,'test',1
 FROM (VALUES
 (1,'2026-10-23T09:00:00Z'::timestamptz,$3::bytea),
 (2,'2026-10-23T06:00:00Z'::timestamptz,$4::bytea),
 (3,'2026-10-22T10:00:00Z'::timestamptz,$5::bytea),
 (4,'2026-10-23T10:00:00Z'::timestamptz,$6::bytea),
 (5,'2026-10-23T09:59:00Z'::timestamptz,NULL::bytea),
 (6,'2026-10-23T09:00:00Z'::timestamptz,$7::bytea))v(n,bt,wallet)`, raw(20, 0xa6), raw(20, 0x61), raw(20, 4), raw(20, 5), raw(20, 6), raw(20, 7), raw(20, 3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(ctx, `INSERT INTO stats_dirty(chain,minute) SELECT DISTINCT chain,date_trunc('minute',block_time) FROM trades WHERE block_num>1000000 ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err = f.st.RefreshStats(ctx, "bsc", f.now); err != nil {
		t.Fatal(err)
	}
	h := f.handler()
	for _, tc := range []struct {
		currency string
		volume   []string
	}{{"usd", []string{"351", "403", "404", "405"}}, {"native", []string{"3.51", "4.03", "4.04", "4.05"}}} {
		var got api.TokenDetail
		getPG(t, h, "/api/v1/tokens/{chain}/{address}", "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061?currency="+tc.currency, 200, &got)
		if len(got.Windows) != 4 || got.AsOf != "2026-10-23T10:00:00.000Z" {
			t.Fatalf("cutoff/windows changed:%+v", got)
		}
		for i, w := range got.Windows {
			if w.Traders == nil || *w.Traders != []int64{3, 4, 5, 6}[i] || w.Volume == nil || *w.Volume != tc.volume[i] {
				t.Fatalf("%s window%d=%+v; want traders%d volume%s", tc.currency, i, w, []int64{3, 4, 5, 6}[i], tc.volume[i])
			}
		}
	}
}
