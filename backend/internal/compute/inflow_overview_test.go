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
)

// H4-4 uses smart-money.md section 4 verbatim: v42, 10:05:30, and a 5m
// half-open window. Identity is (chain,wallet), including unknown USD prices.
func TestOverviewActiveWalletsExample(t *testing.T) {
	s, _ := open(t)
	asOf := day.Add(10*time.Hour + 5*time.Minute + 30*time.Second)
	b := pgapi.New(s, config.Config{}, func() time.Time { return asOf })
	csv := "chain,address\n"
	for _, chain := range []string{"base", "bsc"} {
		for _, wallet := range []byte{10, 11, 12, 13, 14} {
			csv += fmt.Sprintf("%s,0x%040x\n", chain, wallet)
		}
	}
	// Generate the document's numeric version through H3's public lifecycle.
	for v := 2; v <= 42; v++ {
		r, err := b.CreateImport(t.Context(), "example.csv", []byte(csv))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = b.ApplyImport(t.Context(), r.ID); err != nil {
			t.Fatal(err)
		}
		if err = lists.New(s, nil).Step(t.Context(), asOf); err != nil {
			t.Fatal(err)
		}
	}
	for _, chain := range []string{"base", "bsc", "solana"} {
		apply(t, s, chain, 1, asOf.Add(-10*time.Minute), parse.Facts{})
	}
	events := []struct {
		chain     string
		at        time.Duration
		wallet    byte
		side      string
		token     byte
		ambiguous bool
	}{
		{"base", 29 * time.Second, 10, "buy", 80, false},
		{"base", 30 * time.Second, 11, "buy", 80, false},
		{"base", 70 * time.Second, 12, "buy", 80, false},
		{"base", 130 * time.Second, 12, "sell", 81, false},
		{"bsc", 190 * time.Second, 12, "buy", 80, false},
		{"base", 329 * time.Second, 13, "buy", 80, false},
		{"base", 330 * time.Second, 14, "buy", 80, false},
		{"base", 200 * time.Second, 99, "buy", 80, false},
		{"base", 210 * time.Second, 14, "buy", 80, true},
	}
	nums := map[string]uint64{"base": 1, "bsc": 1, "solana": 1}
	for _, e := range events {
		nums[e.chain]++
		tr := h4Trade(1, e.wallet, "1000000")
		tr.Side = e.side
		tr.Token = h4Key(e.token)
		tr.Pool = h4Key(e.token + 10)
		if e.ambiguous {
			tr.Attribution = "ambiguous"
			tr.Wallet = nil
		}
		f := h4Facts(tr)
		f.Tokens[0].Address = tr.Token
		f.Pools[0].ID = tr.Pool
		f.Pools[0].Token = tr.Token
		at := day.Add(10*time.Hour + e.at)
		apply(t, s, e.chain, nums[e.chain], at, f)
		// Replaying the identical fact batch does not create a second economic action.
		apply(t, s, e.chain, nums[e.chain], at, f)
	}
	for _, chain := range []string{"base", "bsc", "solana"} {
		nums[chain]++
		apply(t, s, chain, nums[chain], asOf.Add(time.Second), parse.Facts{})
		if err := s.RefreshCoverage(t.Context(), chain); err != nil {
			t.Fatal(err)
		}
	}
	if err := compute.NewSmTrades(s, []string{"base", "bsc", "solana"}).Step(t.Context(), asOf); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		chain api.Chain
		want  int64
	}{{api.Base, 3}, {api.BSC, 1}, {"", 4}} {
		got, err := b.Overview(t.Context(), tc.chain, "5m")
		if err != nil {
			t.Fatalf("overview %s: %v", tc.chain, err)
		}
		if got.ListVersion != "42" || got.AsOf != "2026-10-23T10:05:30.000Z" || got.ActiveWallets == nil || *got.ActiveWallets != tc.want {
			t.Errorf("chain=%s overview=%+v, want list=42 as_of=10:05:30 active=%d", tc.chain, got, tc.want)
		}
	}
}

func checkH4Text(t *testing.T, field string, got *string, want string) {
	t.Helper()
	actual := "NULL"
	if got != nil {
		actual = *got
	}
	if actual != want {
		t.Errorf("%s=%s, want %s", field, actual, want)
	}
}

// H4-5 uses the two independently worked single-trade-size examples in
// smart-money.md section 6. Unpriced sells never poison a known buy split.
func TestOverviewLargeSmallExample(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		amounts                          []string
		unknownBuy, unknownSell, partial bool
		large, small, lp, sp             string
	}{
		{"strict boundary same wallet", []string{"5000000000", "5000000001", "3000000000", "3000000000"}, false, false, false, "5000.000001", "11000", "skip", "skip"},
		{"amount weighted percentages", []string{"6000000000", "4000000000"}, false, false, false, "6000", "4000", "60", "40"},
		{"unknown buy", []string{"6000000000"}, true, false, false, "NULL", "NULL", "NULL", "NULL"},
		{"unknown sell", []string{"6000000000", "4000000000"}, false, true, false, "6000", "4000", "60", "40"},
		{"no buys", nil, false, false, false, "0", "0", "NULL", "NULL"},
		{"partial", []string{"6000000000"}, false, false, true, "NULL", "NULL", "NULL", "NULL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := open(t)
			now := day.Add(14 * time.Hour)
			h4Members(t, s, now, false)
			at := now.Add(-time.Minute)
			h4Quotes(t, s, at)
			first := now.Add(-11 * time.Minute)
			if tc.partial {
				first = now.Add(-2 * time.Minute)
			}
			apply(t, s, "bsc", 1, first, parse.Facts{})
			f := h4Facts()
			for i, amount := range tc.amounts {
				f.Trades = append(f.Trades, h4Trade(i+1, 1, amount))
			}
			if tc.unknownBuy || tc.unknownSell {
				tr := h4Trade(50, 1, "1000000")
				tr.Pool = h4Key(91)
				tr.QuoteSymbol = "UNKNOWN"
				if tc.unknownSell {
					tr.Side = "sell"
				}
				f.Trades = append(f.Trades, tr)
				f.Pools = append(f.Pools, parse.Pool{ID: h4Key(91), Token: h4Key(80), Quote: h4Key(100), QuoteSymbol: "UNKNOWN", Protocol: "test", Dex: "unknown", Created: true, Status: "active"})
			}
			apply(t, s, "bsc", 2, at, f)
			apply(t, s, "bsc", 3, now.Add(time.Second), parse.Facts{})
			for _, err := range []error{s.FillValues(t.Context(), "bsc", "BNB", first), s.RefreshSmTrades(t.Context(), "bsc", now), s.RefreshCoverage(t.Context(), "bsc")} {
				if err != nil {
					t.Fatal(err)
				}
			}
			b := pgapi.New(s, config.Config{}, func() time.Time { return now })
			got, err := b.Overview(t.Context(), api.BSC, "5m")
			if err != nil {
				t.Fatal(err)
			}
			checkH4Text(t, "large", got.LargeBuyVolumeUSD, tc.large)
			checkH4Text(t, "small", got.SmallBuyVolumeUSD, tc.small)
			if tc.lp != "skip" {
				checkH4Text(t, "large share", got.LargeBuySharePct, tc.lp)
				checkH4Text(t, "small share", got.SmallBuySharePct, tc.sp)
			}
		})
	}
}

// H4-6: 300-100=200, previous 150-50=100, so the change is +100% and
// buy share 300/400=75%. The 5m window has 16 buckets of 18.75 seconds.
func TestOverviewNetInflowBarsAndChange(t *testing.T) {
	s, c := open(t)
	now := day.Add(15 * time.Hour)
	h4Members(t, s, now, false)
	start := now.Add(-5 * time.Minute)
	apply(t, s, "bsc", 1, now.Add(-11*time.Minute), parse.Facts{})
	for i, e := range []struct {
		at           time.Time
		side, amount string
	}{
		{now.Add(-9 * time.Minute), "buy", "150000000"},
		{now.Add(-8 * time.Minute), "sell", "50000000"},
		{start.Add(time.Second), "buy", "300000000"},
		{start.Add(20 * time.Second), "sell", "100000000"},
	} {
		h4Quotes(t, s, e.at.Truncate(time.Minute))
		tr := h4Trade(1, 1, e.amount)
		tr.Side = e.side
		f := parse.Facts{Trades: []parse.Trade{tr}}
		if i == 0 {
			f = h4Facts(tr)
		}
		apply(t, s, "bsc", uint64(i+2), e.at, f)
	}
	six := int16(6)
	apply(t, s, "bsc", 6, start.Add(25*time.Second), parse.Facts{Tokens: []parse.Token{{Address: h4Key(81), Created: true, Symbol: "NEW", Decimals: &six}}})
	apply(t, s, "bsc", 7, now.Add(time.Second), parse.Facts{})
	for _, err := range []error{s.FillValues(t.Context(), "bsc", "BNB", now.Add(-11*time.Minute)), s.RefreshSmTrades(t.Context(), "bsc", now), s.RefreshCoverage(t.Context(), "bsc")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	// Seed only the already-agreed projection persistence boundary; the builder
	// is covered by H4-7. An unreferenced old version must not affect the count.
	if _, err := c.Exec(t.Context(), `INSERT INTO projection_heads VALUES('inflow','bsc','5m',7,$1,2,$1,false)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(t.Context(), `INSERT INTO inflow_rows(projection_version,chain,"window",token_address) VALUES(7,'bsc','5m',$1),(6,'bsc','5m',$2)`, h4Key(80), h4Key(81)); err != nil {
		t.Fatal(err)
	}
	b := pgapi.New(s, config.Config{}, func() time.Time { return now })
	got, err := b.Overview(t.Context(), api.BSC, "5m")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		name string
		got  *string
		want string
	}{{"net", got.NetInflowUSD, "200"}, {"buy", got.BuyVolumeUSD, "300"}, {"sell", got.SellVolumeUSD, "100"}, {"change", got.NetInflowChangePct, "100"}, {"buy share", got.BuySharePct, "75"}} {
		checkH4Text(t, v.name, v.got, v.want)
	}
	if got.TrackedTokens == nil || *got.TrackedTokens != 1 || got.NewTokens == nil || *got.NewTokens != 1 {
		t.Fatalf("tracked/new=%v/%v, want 1/1", got.TrackedTokens, got.NewTokens)
	}
	if len(got.InflowBars) != 16 {
		t.Fatalf("bars=%d, want 16", len(got.InflowBars))
	}
	for i, bar := range got.InflowBars {
		want := "0"
		if i == 0 {
			want = "300"
		}
		if i == 1 {
			want = "-100"
		}
		checkH4Text(t, fmt.Sprintf("bar %d", i), bar, want)
	}
	// An unknown-price sell in bucket 8 makes only that bucket unknown, and
	// makes the complete net/share unknown while preserving the known buy total.
	tr := h4Trade(1, 1, "1")
	tr.Pool = h4Key(91)
	tr.Side = "sell"
	apply(t, s, "bsc", 8, start.Add(151*time.Second), parse.Facts{Pools: []parse.Pool{{ID: h4Key(91), Token: h4Key(80), Quote: h4Key(100), Protocol: "test", Dex: "unknown", Created: true}}, Trades: []parse.Trade{tr}})
	if err := s.RefreshSmTrades(t.Context(), "bsc", now); err != nil {
		t.Fatal(err)
	}
	got, err = b.Overview(t.Context(), api.BSC, "5m")
	if err != nil {
		t.Fatal(err)
	}
	checkH4Text(t, "unknown net", got.NetInflowUSD, "NULL")
	checkH4Text(t, "known buy", got.BuyVolumeUSD, "300")
	checkH4Text(t, "unknown bar 8", got.InflowBars[8], "NULL")
	checkH4Text(t, "preserved bar 0", got.InflowBars[0], "300")
}
