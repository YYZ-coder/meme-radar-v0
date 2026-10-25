package compute_test

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

// H4 fallback regression: expectations are computed directly from raw trades
// joined to the independently persisted member snapshot, never sm_trades/stats.
func TestInflowMatchesRawTrades(t *testing.T) {
	s, c := open(t)
	now := day.Add(20 * time.Hour)
	version := h4Members(t, s, now, true) // wallet 1 active, wallet 2 excluded.
	j, members := h4QueuedThird(t, s, now)
	if err := s.PrepareListBuild(t.Context(), j, members, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateListBuild(t.Context(), j, members, now); err != nil {
		t.Fatal(err)
	}
	version++ // third member 3 is now active alongside 1; 2 remains excluded.
	six := int16(6)
	meta := parse.Facts{}
	changes := []store.MainPoolChange{}
	for i := byte(0); i < 5; i++ {
		token, pool := h4Key(70+i), h4Key(90+i)
		meta.Tokens = append(meta.Tokens, parse.Token{Address: token, Decimals: &six, Created: true})
		meta.Pools = append(meta.Pools, parse.Pool{ID: pool, Token: token, Quote: h4Key(99), QuoteSymbol: "USDC", Dex: "raw-check", Protocol: "test", Created: true, Status: "active"})
		changes = append(changes, store.MainPoolChange{Token: token, Pool: pool, Reason: "liquidity"})
	}
	apply(t, s, "bsc", 1, now.Add(-2*time.Hour), meta)
	rng := rand.New(rand.NewSource(1402026))
	f := parse.Facts{}
	for n := 0; n < 200; n++ {
		token := byte(rng.Intn(5))
		if n < 100 {
			token = byte(rng.Intn(2))
		}
		wallet := byte(1 + rng.Intn(4))
		tr := h4Trade(n+1, wallet, fmt.Sprint((1+rng.Intn(9000))*1000000))
		tr.Token, tr.Pool = h4Key(70+token), h4Key(90+token)
		if rng.Intn(2) == 0 {
			tr.Side = "sell"
		}
		if n%9 == 0 {
			tr.Attribution = "ambiguous"
			tr.Wallet = nil
		}
		if n%7 == 0 {
			tr.Attribution = "inferred"
		}
		if tr.Wallet == nil {
			tr.Attribution = "ambiguous"
		}
		f.Trades = append(f.Trades, tr)
	}
	// The earlier hundred facts have no native quote minute and stay unpriced;
	// the last hundred use a known minute. Both lie inside the one-hour window.
	apply(t, s, "bsc", 2, now.Add(-10*time.Minute), parse.Facts{Trades: f.Trades[:100]})
	apply(t, s, "bsc", 3, now.Add(-time.Minute), parse.Facts{Trades: f.Trades[100:]})
	apply(t, s, "bsc", 4, now.Add(time.Second), parse.Facts{})
	h4Quotes(t, s, now.Add(-time.Minute))
	for _, err := range []error{s.FillValues(t.Context(), "bsc", "BNB", now.Add(-time.Hour)), s.SaveMainPools(t.Context(), "bsc", changes, now.Add(-2*time.Hour)), s.RefreshStats(t.Context(), "bsc", now), s.RefreshSmTrades(t.Context(), "bsc", now), s.RefreshCoverage(t.Context(), "bsc")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	built, err := s.RebuildInflow(t.Context(), "bsc", "1h", now)
	if err != nil {
		t.Fatal(err)
	}
	if built.Rows != 5 {
		t.Fatalf("published rows=%d; want 5", built.Rows)
	}
	var raw, eligible, unknown, ambiguous, excluded int
	if err = c.QueryRow(t.Context(), `SELECT count(*),count(*) FILTER(WHERE t.wallet IS NOT NULL AND t.attribution IN('confirmed','inferred') AND m.address IS NOT NULL AND NOT m.excluded),count(*) FILTER(WHERE value_usd IS NULL),count(*) FILTER(WHERE attribution='ambiguous'),count(*) FILTER(WHERE m.excluded) FROM trades t LEFT JOIN sm_list_members m ON m.version=$1 AND m.chain=t.chain AND m.address=t.wallet WHERE t.chain='bsc'`, version).Scan(&raw, &eligible, &unknown, &ambiguous, &excluded); err != nil {
		t.Fatal(err)
	}
	if raw != 200 || eligible == 0 || unknown != 100 || ambiguous == 0 || excluded == 0 {
		t.Fatalf("fixture coverage raw/eligible/unknown/ambiguous/excluded=%d/%d/%d/%d/%d", raw, eligible, unknown, ambiguous, excluded)
	}
	rows, err := c.Query(t.Context(), `WITH raw AS (
 SELECT t.token_address,count(*) AS trades,CASE WHEN count(*) FILTER(WHERE t.value_usd IS NULL)=0 THEN sum(t.value_usd) END AS volume,
 count(*) FILTER(WHERE m.address IS NOT NULL AND NOT m.excluded AND t.attribution IN('confirmed','inferred')) AS smart,
 CASE WHEN count(*) FILTER(WHERE m.address IS NOT NULL AND NOT m.excluded AND t.attribution IN('confirmed','inferred') AND t.value_usd IS NULL)=0 THEN coalesce(sum(CASE WHEN t.side='buy' THEN t.value_usd ELSE -t.value_usd END) FILTER(WHERE m.address IS NOT NULL AND NOT m.excluded AND t.attribution IN('confirmed','inferred')),0) END AS net
 FROM trades t LEFT JOIN sm_list_members m ON m.version=$2 AND m.chain=t.chain AND m.address=t.wallet
 WHERE t.chain='bsc' AND t.block_time>=$3::timestamptz-interval '1 hour' AND t.block_time<$3 GROUP BY t.token_address
 ) SELECT encode(r.token_address,'hex'),r.trades=raw.trades AND r.sm_trades=raw.smart AND r.volume_usd IS NOT DISTINCT FROM raw.volume AND r.net_inflow_usd IS NOT DISTINCT FROM raw.net,raw.net IS NOT NULL
 FROM inflow_rows r FULL JOIN raw USING(token_address) WHERE r.projection_version=$1`, built.Version, version, now)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	checked, known := 0, 0
	for rows.Next() {
		var token string
		var match, priced bool
		if err := rows.Scan(&token, &match, &priced); err != nil {
			t.Fatal(err)
		}
		if !match {
			t.Errorf("projection differs from raw facts for %s", token)
		}
		checked++
		if priced {
			known++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if known != 3 {
		t.Fatalf("known raw net amounts=%d; want 3, with two tokens carrying unknown facts", known)
	}
	if checked != 5 {
		t.Fatalf("checked %d raw/projection rows; want 5", checked)
	}
}
