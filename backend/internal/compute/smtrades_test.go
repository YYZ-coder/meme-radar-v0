package compute_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/lists"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

func h4Key(tail byte) []byte { b := make([]byte, 20); b[19] = tail; return b }

// h4Members uses the same import/build path as an administrator.
func h4Members(t *testing.T, s *store.Store, now time.Time, excludeSecond bool) int64 {
	t.Helper()
	b := pgapi.New(s, config.Config{}, func() time.Time { return now })
	r, err := b.CreateImport(t.Context(), "h4.csv", []byte("chain,address\nbsc,0x0000000000000000000000000000000000000001\nbsc,0x0000000000000000000000000000000000000002\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.ApplyImport(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	if err = lists.New(s, nil).Step(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if excludeSecond {
		if _, err = b.Exclude(t.Context(), api.BSC, "0x0000000000000000000000000000000000000002", "excluded fixture"); err != nil {
			t.Fatal(err)
		}
		if err = lists.New(s, nil).Step(t.Context(), now); err != nil {
			t.Fatal(err)
		}
		return 3
	}
	return 2
}

func h4Trade(tx int, wallet byte, quote string) parse.Trade {
	tr := trade(tx, h4Key(90), h4Key(80), "1000000", quote, "USDC")
	tr.Wallet = h4Key(wallet)
	tr.PriceNative = "1"
	return tr
}

func h4Facts(trades ...parse.Trade) parse.Facts {
	six := int16(6)
	return parse.Facts{Tokens: []parse.Token{{Address: h4Key(80), Created: true, Symbol: "H4", Decimals: &six}},
		Pools: []parse.Pool{{ID: h4Key(90), Token: h4Key(80), Quote: h4Key(99), QuoteSymbol: "USDC", Protocol: "test", Dex: "test", Created: true, Status: "active"}}, Trades: trades}
}

func h4Quotes(t *testing.T, s *store.Store, at time.Time) {
	t.Helper()
	if err := s.SaveQuoteAssets(t.Context(), []store.QuoteAsset{{Chain: "bsc", Asset: h4Key(99), Symbol: "USDC", Kind: "stable", Decimals: 6}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(t.Context(), []store.QuotePrice{{Symbol: "BNB", Minute: at, USD: "100", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
}

// H4-1's agreed seam is the compute-owned sm_trades table. Expectations come
// from membership/attribution rules and the strict single-trade $5,000 limit.
func TestSmTradesCopyMemberTrades(t *testing.T) {
	s, c := open(t)
	now := day.Add(12 * time.Hour)
	version := h4Members(t, s, now, true)
	at := now.Add(-time.Minute)
	h4Quotes(t, s, at)
	ambiguous := h4Trade(2, 1, "5000000000")
	ambiguous.Attribution = "ambiguous"
	ambiguous.Wallet = nil
	apply(t, s, "bsc", 1, at, h4Facts(h4Trade(1, 1, "5000000000"), ambiguous, h4Trade(3, 2, "5000000000"), h4Trade(4, 3, "5000000000")))
	if err := s.FillValues(t.Context(), "bsc", "BNB", at); err != nil {
		t.Fatal(err)
	}
	step := compute.NewSmTrades(s, []string{"bsc"})
	if err := step.Step(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=$1`, version); got != "1" {
		t.Fatalf("eligible member trades=%s, want 1", got)
	}
	if got := one(t, c, `SELECT is_large::text FROM sm_trades WHERE version=$1 AND block_num=1`, version); got != "false" {
		t.Fatalf("$5000 is_large=%s, want false", got)
	}
	inferred := h4Trade(1, 1, "5000000001")
	inferred.Attribution = "inferred"
	sell := h4Trade(2, 1, "6000000000")
	sell.Side = "sell"
	apply(t, s, "bsc", 2, at.Add(time.Second), parse.Facts{Trades: []parse.Trade{inferred, sell}})
	apply(t, s, "bsc", 3, at.Add(6*time.Minute), parse.Facts{Trades: []parse.Trade{h4Trade(1, 1, "1")}})
	if err := s.FillValues(t.Context(), "bsc", "BNB", at); err != nil {
		t.Fatal(err)
	}
	if err := step.Step(t.Context(), now.Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		block, tx int
		want      string
	}{{2, 1, "true"}, {2, 2, "NULL"}, {3, 1, "NULL"}} {
		got := one(t, c, `SELECT is_large::text FROM sm_trades WHERE version=$1 AND block_num=$2 AND tx_index=$3`, version, tc.block, tc.tx)
		if got != tc.want {
			t.Errorf("%s is_large=%s, want %s", fmt.Sprint(tc.block, "/", tc.tx), got, tc.want)
		}
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=$1`, version); got != "4" {
		t.Fatalf("eligible member trades=%s, want 4", got)
	}
}

// H4-2a observes FillValues' promised dirty-minute boundary, independently of
// whether any smart-money copy already exists. One priced minute means one key.
func TestFillValuesMarksSmTradesDirty(t *testing.T) {
	s, c := open(t)
	at := day.Add(12 * time.Hour)
	h4Quotes(t, s, at)
	apply(t, s, "bsc", 1, at.Add(time.Second), h4Facts(h4Trade(1, 1, "5000000001")))
	if got := one(t, c, `SELECT count(*)::text FROM smtrades_dirty`); got != "0" {
		t.Fatalf("initial dirty minutes=%s, want 0", got)
	}
	if err := s.FillValues(t.Context(), "bsc", "BNB", at); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM smtrades_dirty WHERE chain='bsc' AND minute=$1`, at); got != "1" {
		t.Fatalf("repriced dirty minute count=%s, want 1", got)
	}
	if err := s.FillValues(t.Context(), "bsc", "BNB", at); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM smtrades_dirty`); got != "1" {
		t.Fatalf("duplicate dirty minutes=%s, want 1", got)
	}
}

// H4-2b checks the persisted copy through unchanged fact writers. At $100/BNB,
// 5000.000001 USD is 50.00000001 native; no parse timestamp changes on repricing.
func TestSmTradesFollowReparseAndPricing(t *testing.T) {
	s, c := open(t)
	now := day.Add(13 * time.Hour)
	version := h4Members(t, s, now, false)
	at := now.Add(-10 * time.Minute)
	h4Quotes(t, s, at)
	apply(t, s, "bsc", 1, at, h4Facts(h4Trade(1, 1, "5000000000")))
	late := at.Add(6 * time.Minute)
	apply(t, s, "bsc", 2, late, parse.Facts{Trades: []parse.Trade{h4Trade(1, 1, "5000000001")}})
	step := compute.NewSmTrades(s, []string{"bsc"})
	if err := s.FillValues(t.Context(), "bsc", "BNB", at); err != nil {
		t.Fatal(err)
	}
	if err := step.Step(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT value_usd::text FROM sm_trades WHERE version=$1 AND block_num=2`, version); got != "NULL" {
		t.Fatalf("unpriced value=%s, want NULL", got)
	}
	if err := s.SaveQuotePrices(t.Context(), []store.QuotePrice{{Symbol: "BNB", Minute: late, USD: "100", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.FillValues(t.Context(), "bsc", "BNB", at); err != nil {
		t.Fatal(err)
	}
	if err := step.Step(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT concat_ws(' ',trim_scale(value_usd)::text,trim_scale(value_native)::text,is_large::text) FROM sm_trades WHERE version=$1 AND block_num=2`, version); got != "5000.000001 50.00000001 true" {
		t.Fatalf("after pricing copy=%s, want 5000.000001 50.00000001 true", got)
	}
	if err := s.ApplyParsed(t.Context(), store.Parsed{Chain: "bsc", Num: 1, Time: at, Version: 2, Facts: parse.Facts{Trades: []parse.Trade{h4Trade(1, 3, "1000000")}}}); err != nil {
		t.Fatal(err)
	}
	if err := step.Step(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=$1 AND block_num=1`, version); got != "0" {
		t.Fatalf("reparsed former member trades=%s, want 0", got)
	}
	if got := one(t, c, `SELECT count(*)::text FROM smtrades_dirty WHERE chain='bsc'`); got != "0" {
		t.Errorf("consumed dirty minutes=%s, want 0", got)
	}
	if got := one(t, c, `SELECT count(*)::text FROM compute_marks WHERE chain='bsc' AND name='smtrades'`); got != "1" {
		t.Errorf("copy watermark=%s, want 1", got)
	}
}
