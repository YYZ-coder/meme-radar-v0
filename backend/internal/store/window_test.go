package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
	"github.com/meme-radar/radar/migrations"
)

var windowStart = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

func applyWindowBlock(t *testing.T, s *store.Store, chain string, num uint64, at time.Time, facts parse.Facts) {
	t.Helper()
	ctx := context.Background()
	if err := s.Commit(ctx, store.Commit{Chain: chain, Block: store.Block{
		Num: num, Hash: []byte{byte(num), byte(num >> 8), 1}, ParentHash: []byte{byte(num - 1), byte((num - 1) >> 8), 1},
		Time: at, Status: store.StatusConfirmed, TxCount: 1, Payload: []byte(`{}`), Source: "test",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: chain, Num: num, Time: at, Version: 1, Facts: facts}); err != nil {
		t.Fatal(err)
	}
}

func TestChainAsOfAndCoverage(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for n := 1; n <= 10; n++ {
		if n != 6 {
			applyWindowBlock(t, s, "base", uint64(n), windowStart.Add(time.Duration(n)*10*time.Second), parse.Facts{})
		}
	}
	if err := compute.NewCoverage(s, []string{"base"}).Step(ctx); err != nil {
		t.Fatal(err)
	}
	// H1-1's independent worked examples: ranges 10:00:10–50 and
	// 10:01:10–40; crossing the missing sixth block cannot be complete.
	for _, now := range []time.Time{windowStart.Add(5 * time.Minute), windowStart.Add(80 * time.Second)} {
		got, ok, err := s.ChainAsOf(ctx, "base", now)
		want := windowStart.Add(time.Minute)
		if err != nil || !ok || !got.Equal(want) {
			t.Errorf("ChainAsOf(%s) = %s, %t, %v; want %s, true", now, got, ok, err, want)
		}
	}
	for _, tc := range []struct {
		from, to int
		complete bool
	}{{15, 45, true}, {15, 75, false}, {0, 30, false}, {75, 95, true}} {
		got, err := s.Coverage(ctx, "base", windowStart.Add(time.Duration(tc.from)*time.Second), windowStart.Add(time.Duration(tc.to)*time.Second))
		if err != nil || got != tc.complete {
			t.Errorf("Coverage(+%ds,+%ds) = %t, %v; want %t", tc.from, tc.to, got, err, tc.complete)
		}
	}
	if _, ok, err := s.ChainAsOf(ctx, "solana", windowStart); err != nil || ok {
		t.Errorf("ChainAsOf(empty chain) = _, %t, %v; want false", ok, err)
	}
}

// seedWindowBlocks writes parsed, consecutive blocks at ten-second spacing,
// plus blocks at each fact's exact time. It never inserts derived rows.
func seedWindowBlocks(t *testing.T, s *store.Store, chain string, from, to time.Time, facts map[time.Time]parse.Facts, missing ...time.Time) {
	t.Helper()
	times := map[time.Time]bool{}
	for at := from; !at.After(to); at = at.Add(10 * time.Second) {
		times[at] = true
	}
	for at := range facts {
		times[at] = true
	}
	ordered := make([]time.Time, 0, len(times))
	for at := range times {
		ordered = append(ordered, at)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })
	skip := map[time.Time]bool{}
	for _, at := range missing {
		skip[at] = true
	}
	for n, at := range ordered {
		if skip[at] {
			continue
		}
		applyWindowBlock(t, s, chain, uint64(n+1), at, facts[at])
	}
}

func windowTrade(tx int, pool, quote, price, wallet string) parse.Trade {
	return parse.Trade{Tx: tx, Ix: 1, TxHash: []byte{byte(tx), 7}, Pool: []byte(pool), Token: []byte("M"), Wallet: []byte(wallet),
		Attribution: "confirmed", Side: "buy", TokenAmount: "1000000", QuoteAmount: quote, QuoteSymbol: "SOL",
		PriceNative: price, ValueNative: price, Decoder: "test", Version: 1}
}

func runWindowCompute(t *testing.T, s *store.Store, now time.Time) {
	t.Helper()
	ctx := context.Background()
	chains := []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}
	for _, step := range []func() error{
		func() error { return compute.NewValues(s, chains).Step(ctx, now.Add(-24*time.Hour)) },
		func() error { return compute.NewLiquidity(s, chains).Step(ctx, now) },
		func() error { return compute.NewMarket(s, chains).Step(ctx, now, true) },
		func() error { return compute.NewStats(s, []string{"solana"}).Step(ctx, now) },
		func() error { return compute.NewCoverage(s, []string{"solana"}).Step(ctx) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
}

func sumWindowFixture(t *testing.T, unpriced bool, missing ...time.Time) *store.Store {
	t.Helper()
	ctx := context.Background()
	s := open(t)
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: []byte("wsol"), Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	var prices []store.QuotePrice
	for n := -1; n <= 5; n++ {
		prices = append(prices, store.QuotePrice{Symbol: "SOL", Minute: windowStart.Add(time.Duration(n) * time.Minute), USD: "100", Source: "binance"})
	}
	if err := s.SaveQuotePrices(ctx, prices); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	facts := map[time.Time]parse.Facts{
		windowStart.Add(-time.Minute): {
			Tokens: []parse.Token{{Address: []byte("M"), Created: true, Symbol: "M", Decimals: &six}},
			Pools: []parse.Pool{
				{ID: []byte("A"), Token: []byte("M"), Quote: []byte("wsol"), QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"},
				{ID: []byte("B"), Token: []byte("M"), Quote: []byte("wsol"), QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "other", Created: true, Status: "active"},
				{ID: []byte("C"), Token: []byte("M"), Quote: []byte("unknown"), Protocol: "pumpswap", Dex: "other", Created: true, Status: "active"},
			},
		},
		windowStart.Add(5 * time.Second):               {Trades: []parse.Trade{windowTrade(1, "A", "1000000000", "1", "w1")}},
		windowStart.Add(15 * time.Second):              {Trades: []parse.Trade{windowTrade(1, "A", "500000000", "0.5", "w2")}},
		windowStart.Add(3*time.Minute + 5*time.Second): {Trades: []parse.Trade{windowTrade(1, "B", "2000000000", "2", "w3")}},
	}
	facts[windowStart.Add(15*time.Second)].Trades[0].Side = "sell"
	if unpriced {
		unknown := windowTrade(1, "C", "7", "7", "w4")
		unknown.ValueNative = ""
		facts[windowStart.Add(4*time.Minute+5*time.Second)] = parse.Facts{Trades: []parse.Trade{unknown}}
	}
	seedWindowBlocks(t, s, "solana", windowStart.Add(-time.Minute), windowStart.Add(5*time.Minute+30*time.Second), facts, missing...)
	runWindowCompute(t, s, windowStart.Add(5*time.Minute+30*time.Second))
	return s
}

func decimalValue(p *string) string {
	if p == nil {
		return "NULL"
	}
	return *p
}

func TestTokenWindowsSumMinuteStats(t *testing.T) {
	ctx := context.Background()
	for _, unpriced := range []bool{true, false} {
		s := sumWindowFixture(t, unpriced)
		for _, native := range []bool{false, true} {
			got, err := s.TokenWindows(ctx, "solana", []byte("M"), windowStart.Add(5*time.Minute), []time.Duration{time.Minute, 5 * time.Minute, time.Hour}, native)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 3 {
				t.Fatalf("TokenWindows(unpriced=%t,native=%t) returned %d windows; want 3", unpriced, native, len(got))
			}
			counts := [3]int64{4, 3, 1}
			amounts := [4]string{"NULL", "NULL", "50", "NULL"}
			if native {
				amounts[2] = "0.5"
			}
			if !unpriced {
				counts = [3]int64{3, 2, 1}
				amounts = [4]string{"350", "300", "50", "250"}
				if native {
					amounts = [4]string{"3.5", "3", "0.5", "2.5"}
				}
			}
			for i, w := range got {
				wantCounts, wantAmounts := counts, amounts
				if i == 0 {
					wantCounts = [3]int64{0, 0, 0}
					wantAmounts = [4]string{"0", "0", "0", "0"}
					if unpriced {
						wantCounts = [3]int64{1, 1, 0}
						wantAmounts = [4]string{"NULL", "NULL", "0", "NULL"}
					}
				}
				if w.Window != []time.Duration{time.Minute, 5 * time.Minute, time.Hour}[i] || w.Complete != (i < 2) || [3]int64{w.Trades, w.Buys, w.Sells} != wantCounts {
					t.Errorf("window[%d], unpriced=%t native=%t: duration=%s complete=%t counts=%v; want complete=%t counts=%v", i, unpriced, native, w.Window, w.Complete, [3]int64{w.Trades, w.Buys, w.Sells}, i < 2, wantCounts)
				}
				if actual := [4]string{decimalValue(w.Volume), decimalValue(w.BuyVolume), decimalValue(w.SellVolume), decimalValue(w.NetInflow)}; actual != wantAmounts {
					t.Errorf("window[%d], unpriced=%t native=%t: amounts=%v; want %v (H1-2 independent worked example)", i, unpriced, native, actual, wantAmounts)
				}
			}
		}
	}
}

func TestTokenWindowsCountDistinctWallets(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	six := int16(6)
	trades := []parse.Trade{
		windowTrade(1, "A", "1000000000", "1", "w1"),
		windowTrade(2, "A", "1000000000", "1", "w1"),
		windowTrade(3, "A", "1000000000", "1", "w2"),
		windowTrade(4, "A", "1000000000", "1", ""),
	}
	trades[3].Wallet = nil
	trades[3].Attribution = "ambiguous"
	seedWindowBlocks(t, s, "solana", windowStart.Add(-time.Minute), windowStart.Add(6*time.Minute), map[time.Time]parse.Facts{
		windowStart.Add(-time.Minute):    {Tokens: []parse.Token{{Address: []byte("M"), Created: true, Decimals: &six}}, Pools: []parse.Pool{{ID: []byte("A"), Token: []byte("M"), Quote: []byte("wsol"), QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"}}},
		windowStart.Add(5 * time.Second): {Trades: trades},
	})
	runWindowCompute(t, s, windowStart.Add(6*time.Minute))
	got, err := s.TokenWindows(ctx, "solana", []byte("M"), windowStart.Add(5*time.Minute), []time.Duration{5 * time.Minute, time.Minute}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d windows; want 2", len(got))
	}
	for i, want := range []int64{2, 0} {
		if !got[i].Complete || got[i].Traders == nil || *got[i].Traders != want {
			t.Errorf("window[%d] complete=%t traders=%v; want complete=true, traders=%d (w1 twice, w2 once, NULL excluded)", i, got[i].Complete, got[i].Traders, want)
		}
	}
}

func TestTokenWindowsPriceChange(t *testing.T) {
	ctx := context.Background()
	var fixture struct {
		PriceCases []struct {
			Name         string `json:"name"`
			InitialPool  string `json:"initial_pool"`
			SwitchSecond int    `json:"switch_second"`
			Trades       []struct {
				Second int    `json:"second"`
				Pool   string `json:"pool"`
				Quote  string `json:"quote_atomic"`
				Price  string `json:"price_native"`
			} `json:"trades"`
		} `json:"price_cases"`
	}
	input, err := os.ReadFile("../../testdata/expect/window_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(input, &fixture); err != nil {
		t.Fatal(err)
	}
	expectedJSON, err := exec.Command("python3", "../../testdata/expect/expect_window.py").CombinedOutput()
	if err != nil {
		t.Fatalf("independent Decimal script: %v: %s", err, expectedJSON)
	}
	var expected struct {
		Changes       map[string][]*string `json:"price_changes"`
		NativeChanges map[string][]*string `json:"native_price_changes"`
	}
	if err = json.Unmarshal(expectedJSON, &expected); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.PriceCases {
		t.Run(tc.Name, func(t *testing.T) {
			s := open(t)
			if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: []byte("wsol"), Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
				t.Fatal(err)
			}
			var prices []store.QuotePrice
			for m := -3; m <= 5; m++ {
				prices = append(prices, store.QuotePrice{Symbol: "SOL", Minute: windowStart.Add(time.Duration(m) * time.Minute), USD: "100", Source: "binance"})
			}
			if err := s.SaveQuotePrices(ctx, prices); err != nil {
				t.Fatal(err)
			}
			six := int16(6)
			created := windowStart.Add(-3 * time.Minute)
			facts := map[time.Time]parse.Facts{created: {Tokens: []parse.Token{{Address: []byte("M"), Created: true, Decimals: &six}}, Pools: []parse.Pool{
				{ID: []byte("A"), Token: []byte("M"), Quote: []byte("wsol"), QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"},
				{ID: []byte("B"), Token: []byte("M"), Quote: []byte("wsol"), QuoteSymbol: "SOL", Protocol: "pumpswap", Dex: "other", Created: true, Status: "active"},
			}}}
			for i, tr := range tc.Trades {
				at := windowStart.Add(time.Duration(tr.Second) * time.Second)
				f := facts[at]
				f.Trades = append(f.Trades, windowTrade(i+1, tr.Pool, tr.Quote, tr.Price, "w"))
				facts[at] = f
			}
			seedWindowBlocks(t, s, "solana", created, windowStart.Add(5*time.Minute+30*time.Second), facts)
			if err := s.SaveMainPools(ctx, "solana", []store.MainPoolChange{{Token: []byte("M"), Pool: []byte(tc.InitialPool), Reason: "liquidity"}}, created); err != nil {
				t.Fatal(err)
			}
			if tc.SwitchSecond != 0 {
				if err := s.SaveMainPools(ctx, "solana", []store.MainPoolChange{{Token: []byte("M"), From: []byte("B"), Pool: []byte("A"), Reason: "liquidity"}}, windowStart.Add(time.Duration(tc.SwitchSecond)*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			runWindowCompute(t, s, windowStart.Add(5*time.Minute+30*time.Second))
			for _, native := range []bool{false, true} {
				windows, err := s.TokenWindows(ctx, "solana", []byte("M"), windowStart.Add(5*time.Minute), []time.Duration{5 * time.Minute, time.Minute}, native)
				if err != nil {
					t.Fatal(err)
				}
				if len(windows) != 2 {
					t.Fatalf("got %d windows; want 2", len(windows))
				}
				wants := expected.Changes[tc.Name]
				if native {
					wants = expected.NativeChanges[tc.Name]
				}
				for i, want := range wants {
					if decimalValue(windows[i].PriceChangePct) != decimalValue(want) {
						t.Errorf("native=%t window[%d] price change=%s; want independent Decimal %s", native, i, decimalValue(windows[i].PriceChangePct), decimalValue(want))
					}
				}
			}
		})
	}
}

func TestTokenWindowsPartialCoverage(t *testing.T) {
	ctx := context.Background()
	complete := sumWindowFixture(t, true)
	partial := sumWindowFixture(t, true, windowStart.Add(2*time.Minute))
	for _, native := range []bool{false, true} {
		full, err := complete.TokenWindows(ctx, "solana", []byte("M"), windowStart.Add(5*time.Minute), []time.Duration{5 * time.Minute}, native)
		if err != nil {
			t.Fatal(err)
		}
		lower, err := partial.TokenWindows(ctx, "solana", []byte("M"), windowStart.Add(5*time.Minute), []time.Duration{5 * time.Minute}, native)
		if err != nil {
			t.Fatal(err)
		}
		if len(full) != 1 || len(lower) != 1 {
			t.Fatalf("full=%d partial=%d; want one each", len(full), len(lower))
		}
		if !full[0].Complete || lower[0].Complete {
			t.Errorf("coverage: full=%t partial=%t; want true,false", full[0].Complete, lower[0].Complete)
		}
		full[0].Complete = false
		if !reflect.DeepEqual(lower[0], full[0]) {
			t.Errorf("native=%t: partial coverage erased or changed observed data: got=%+v want=%+v", native, lower[0], full[0])
		}
	}
}

// openBeforeWindowMigration models a persisted 0010 database. Only this local
// migration fixture writes legacy derived rows directly; source facts still go
// through Commit/ApplyParsed and Values, and the actual upgrade uses Migrate.
func openBeforeWindowMigration(t *testing.T) (*store.Store, *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	url := testdb.New(t)
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	if _, err := c.Exec(ctx, `CREATE TABLE schema_migrations(name text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		// Preserve the pre-0011 main-pool and candle schema. Preload the
		// independent additive migrations required by current FillValues/ingest
		// writes; extend this list when that path gains another dependency.
		if name >= "0011" && name != "0013_sm_lists.sql" && name != "0014_inflow.sql" && name != "0015_balances.sql" {
			continue
		}
		sql, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("legacy migration %s: %v", name, err)
		}
		if _, err = c.Exec(ctx, `INSERT INTO schema_migrations(name)VALUES($1)`, name); err != nil {
			t.Fatal(err)
		}
	}
	return s, c
}

func TestMigrationFixesGraduationSwitchTimes(t *testing.T) {
	ctx := context.Background()
	s, c := openBeforeWindowMigration(t)
	now := time.Now().UTC().Truncate(time.Minute)
	base := now.Add(-time.Hour)
	old := now.Add(-4 * 24 * time.Hour)
	six := int16(6)
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: []byte("wsol"), Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: base, USD: "100", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	for i, tr := range []struct {
		second             int
		pool, quote, price string
	}{
		{10, "A", "10000000", "0.01"}, {20, "A", "12000000", "0.012"},
		{45, "B", "20000000", "0.02"}, {50, "A", "90000000", "0.09"}, {55, "B", "22000000", "0.022"},
	} {
		f := parse.Facts{Trades: []parse.Trade{windowTrade(1, tr.pool, tr.quote, tr.price, "w")}}
		if i == 0 {
			f.Tokens = []parse.Token{{Address: []byte("M"), Created: true, Decimals: &six}}
			f.Pools = []parse.Pool{
				{ID: []byte("A"), Token: []byte("M"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"},
				{ID: []byte("B"), Token: []byte("M"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"},
			}
		}
		applyWindowBlock(t, s, "solana", uint64(i+1), base.Add(time.Duration(tr.second)*time.Second), f)
	}
	for i, x := range []struct {
		token, pool string
		at          time.Time
	}{{"G", "P2", base.Add(90 * time.Second)}, {"OG", "OP2", old.Add(90 * time.Second)}} {
		applyWindowBlock(t, s, "solana", uint64(10+i), x.at, parse.Facts{
			Tokens: []parse.Token{{Address: []byte(x.token), Created: true, Decimals: &six}},
			Pools:  []parse.Pool{{ID: []byte(x.pool), Token: []byte(x.token), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"}},
		})
	}
	if err := compute.NewValues(s, []compute.ChainNative{{Chain: "solana", Symbol: "SOL"}}).Step(ctx, old); err != nil {
		t.Fatal(err)
	}
	// These are independently specified old-writer results, not results from the
	// new algorithm. Before upgrade the 1m candle uses B for the entire minute.
	for _, x := range []struct {
		token, from, to, reason string
		at                      time.Time
	}{
		{"M", "", "A", "liquidity", base.Add(-time.Minute)},
		{"M", "A", "B", "liquidity", base.Add(40 * time.Second)},
		{"G", "", "P1", "liquidity", base},
		{"G", "P1", "P2", "graduation", base.Add(5 * time.Minute)},
		{"OG", "", "OP1", "liquidity", old},
		{"OG", "OP1", "OP2", "graduation", old.Add(5 * time.Minute)},
		{"O", "OA", "OB", "liquidity", old.Add(40 * time.Second)},
		{"F", "", "FA", "liquidity", base.Add(10 * time.Minute)},
	} {
		var from []byte
		if x.from != "" {
			from = []byte(x.from)
		}
		if _, err := c.Exec(ctx, `INSERT INTO pool_switches(chain,token_address,t,from_pool,to_pool,reason)VALUES('solana',$1,$2,$3,$4,$5)`, []byte(x.token), x.at, from, []byte(x.to), x.reason); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Exec(ctx, `INSERT INTO candles(chain,token_address,interval,t,pool_id,o,h,l,c,o_native,h_native,l_native,c_native,v,closed)
 VALUES('solana','M','1m',$1,'B',2,2.2,2,2.2,0.02,0.022,0.02,0.022,15.4,true)`, base); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, `DELETE FROM stats_dirty;
 INSERT INTO compute_marks(chain,name,at)SELECT 'solana','stats',max(parsed_at)FROM chain_blocks`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		token string
		at    time.Time
	}{{"G", base.Add(90 * time.Second)}, {"OG", old.Add(90 * time.Second)}} {
		var got time.Time
		if err := c.QueryRow(ctx, `SELECT t FROM pool_switches WHERE token_address=$1 AND reason='graduation'`, []byte(x.token)).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !got.Equal(x.at) {
			t.Errorf("%s graduation=%s; want %s", x.token, got, x.at)
		}
	}
	rows, err := c.Query(ctx, `SELECT minute FROM stats_dirty ORDER BY minute`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{base, base.Add(time.Minute), base.Add(2 * time.Minute), base.Add(3 * time.Minute), base.Add(4 * time.Minute), base.Add(5 * time.Minute)}
	if len(got) != len(want) {
		t.Errorf("dirty=%v; want only recent switch minute and corrected interval %v", got, want)
	} else {
		for i := range want {
			if !got[i].Equal(want[i]) {
				t.Errorf("dirty[%d]=%s; want %s", i, got[i], want[i])
			}
		}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if err := compute.NewStats(s, []string{"solana"}).Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	// Hand calculation: A1,A1.2 before 40s, B2,B2.2 afterwards. A9 is
	// volume-only: OHLC 1/2.2/1/2.2, V=1+1.2+2+9+2.2=15.4.
	for _, interval := range []string{"1m", "5m", "1h", "1d"} {
		got := one(t, c, `SELECT concat_ws(' ',trim_scale(o),trim_scale(h),trim_scale(l),trim_scale(c),trim_scale(v),encode(pool_id,'hex')) FROM candles WHERE token_address='M' AND interval=$1`, interval)
		if got != "1 2.2 1 2.2 15.4 42" {
			t.Errorf("rebuilt %s=%s; want 1 2.2 1 2.2 15.4 42", interval, got)
		}
	}
	if got := one(t, c, `SELECT count(*)::text FROM stats_dirty`); got != "0" {
		t.Errorf("dirty after Stats=%s; want 0", got)
	}
}

func tradeCandleFixture(t *testing.T, partial bool) *store.Store {
	t.Helper()
	ctx := context.Background()
	s := open(t)
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: []byte("wsol"), Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: windowStart, USD: "100", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	created := windowStart.Add(-10 * time.Second)
	f := map[time.Time]parse.Facts{created: {
		Tokens: []parse.Token{{Address: []byte("M"), Created: true, Decimals: &six, TotalSupply: "1000000000000000"}},
		Pools: []parse.Pool{
			{ID: []byte("A"), Token: []byte("M"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active", ReserveToken: "1000000", ReserveQuote: "100000000000"},
			{ID: []byte("B"), Token: []byte("M"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active", ReserveToken: "1000000", ReserveQuote: "1000000000"},
		},
	}}
	for i, tr := range []struct {
		second             int
		pool, quote, price string
	}{
		{5, "A", "10000000", "0.01"}, {10, "A", "15000000", "0.015"}, {15, "B", "90000000", "0.09"},
		{20, "A", "8000000", "0.008"}, {31, "A", "12000000", "0.012"},
	} {
		f[windowStart.Add(time.Duration(tr.second)*time.Second)] = parse.Facts{Trades: []parse.Trade{windowTrade(i+1, tr.pool, tr.quote, tr.price, "w")}}
	}
	var missing []time.Time
	if partial {
		missing = []time.Time{windowStart.Add(30 * time.Second)}
	}
	seedWindowBlocks(t, s, "solana", created, windowStart.Add(70*time.Second), f, missing...)
	if err := s.SaveMainPools(ctx, "solana", []store.MainPoolChange{{Token: []byte("M"), Pool: []byte("A"), Reason: "liquidity"}}, created); err != nil {
		t.Fatal(err)
	}
	runWindowCompute(t, s, windowStart.Add(70*time.Second))
	return s
}

func TestTradeCandlesFromTrades(t *testing.T) {
	ctx := context.Background()
	s := tradeCandleFixture(t, false)
	now := windowStart.Add(45 * time.Second)
	for _, native := range []bool{false, true} {
		got, err := s.TokenCandles(ctx, "solana", []byte("M"), "30s", "price", native, windowStart, windowStart.Add(time.Minute), 10, now)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Complete || got.EmptyReason != "" || len(got.Bars) != 2 {
			t.Errorf("30s set=%+v; want complete, 2 bars", got)
			continue
		}
		want := [][5]string{{"1", "1.5", "0.8", "0.8", "12.3"}, {"1.2", "1.2", "1.2", "1.2", "1.2"}}
		if native {
			want = [][5]string{{"0.01", "0.015", "0.008", "0.008", "12.3"}, {"0.012", "0.012", "0.012", "0.012", "1.2"}}
		}
		for i, b := range got.Bars {
			values := [5]string{decimalValue(b.O), decimalValue(b.H), decimalValue(b.L), decimalValue(b.C), decimalValue(b.V)}
			if values != want[i] || !b.T.Equal(windowStart.Add(time.Duration(i)*30*time.Second)) || b.Closed != (i == 0) {
				t.Errorf("native=%t bar[%d]=%+v values=%v; want %v closed=%t", native, i, b, values, want[i], i == 0)
			}
		}
	}
	got, err := s.TokenCandles(ctx, "solana", []byte("M"), "1s", "price", false, windowStart, windowStart.Add(time.Minute), 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bars) != 4 {
		t.Errorf("1s bars=%d; want 4 main-pool buckets", len(got.Bars))
	} else {
		for i, second := range []int{5, 10, 20, 31} {
			if !got.Bars[i].T.Equal(windowStart.Add(time.Duration(second) * time.Second)) {
				t.Errorf("1s bar[%d] time=%s; want +%d", i, got.Bars[i].T, second)
			}
		}
	}
	got, err = s.TokenCandles(ctx, "solana", []byte("M"), "1s", "price", false, windowStart, windowStart.Add(time.Minute), 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bars) != 2 || !got.Bars[0].T.Equal(windowStart.Add(20*time.Second)) || !got.Bars[1].T.Equal(windowStart.Add(31*time.Second)) {
		t.Errorf("limited bars=%+v; want latest +20,+31 ascending", got.Bars)
	}
	_, err = s.TokenCandles(ctx, "solana", []byte("M"), "30s", "price", false, now.Add(-24*time.Hour-time.Second), now, 10, now)
	if !errors.Is(err, store.ErrRangeTooLong) {
		t.Errorf("old range err=%v; want ErrRangeTooLong", err)
	}
}

func TestTokenCandlesMetricAndGaps(t *testing.T) {
	t.Run("stored_intervals_and_market_cap", func(t *testing.T) {
		ctx := context.Background()
		s := tradeCandleFixture(t, false)
		now := windowStart.Add(45 * time.Second)
		for _, iv := range []struct {
			name   string
			t      time.Time
			closed bool
		}{
			{"1m", windowStart, false}, {"5m", windowStart, false}, {"1h", windowStart, false}, {"1d", windowStart.Truncate(24 * time.Hour), false},
		} {
			set, err := s.TokenCandles(ctx, "solana", []byte("M"), iv.name, "price", false, iv.t, windowStart.Add(time.Minute), 10, now)
			if err != nil {
				t.Errorf("%s: %v", iv.name, err)
				continue
			}
			if len(set.Bars) != 1 {
				t.Errorf("%s bars=%d; want 1 persisted bar", iv.name, len(set.Bars))
				continue
			}
			b := set.Bars[0]
			got := [5]string{decimalValue(b.O), decimalValue(b.H), decimalValue(b.L), decimalValue(b.C), decimalValue(b.V)}
			if got != [5]string{"1", "1.5", "0.8", "1.2", "13.5"} || !b.T.Equal(iv.t) || b.Closed != iv.closed {
				t.Errorf("%s bar=%+v values=%v; want persisted OHLC 1/1.5/0.8/1.2, v13.5, open", iv.name, b, got)
			}
			later, err := s.TokenCandles(ctx, "solana", []byte("M"), iv.name, "price", false, iv.t, windowStart.Add(time.Minute), 10, windowStart.Add(24*time.Hour))
			if err != nil || len(later.Bars) != 1 || !later.Bars[0].Closed {
				t.Errorf("%s later query=%+v err=%v; want closed by requested now", iv.name, later, err)
			}
		}
		data, err := exec.Command("python3", "../../testdata/expect/expect_window.py").Output()
		if err != nil {
			t.Fatal(err)
		}
		var expected struct {
			MarketCap map[string][][4]string `json:"market_cap"`
		}
		if err := json.Unmarshal(data, &expected); err != nil {
			t.Fatal(err)
		}
		for _, native := range []bool{false, true} {
			unit := "usd"
			if native {
				unit = "native"
			}
			set, err := s.TokenCandles(ctx, "solana", []byte("M"), "30s", "market_cap", native, windowStart, windowStart.Add(time.Minute), 10, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(set.Bars) != 2 {
				t.Errorf("market cap bars=%d; want 2", len(set.Bars))
				continue
			}
			for i, b := range set.Bars {
				got := [4]string{decimalValue(b.O), decimalValue(b.H), decimalValue(b.L), decimalValue(b.C)}
				if got != expected.MarketCap[unit][i] {
					t.Errorf("%s market_cap[%d]=%v; want independent Decimal %v", unit, i, got, expected.MarketCap[unit][i])
				}
				wantVolume := []string{"12.3", "1.2"}[i]
				if decimalValue(b.V) != wantVolume {
					t.Errorf("%s v=%s; want USD %s", unit, decimalValue(b.V), wantVolume)
				}
			}
		}
	})
}

func availabilityCandleFixture(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	s := open(t)
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: []byte("wsol"), Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{
		{Symbol: "SOL", Minute: windowStart.Add(-4 * time.Minute), USD: "100", Source: "binance"},
		{Symbol: "SOL", Minute: windowStart.Add(6 * time.Minute), USD: "100", Source: "binance"},
	}); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	created := windowStart.Add(-10 * time.Second)
	f := map[time.Time]parse.Facts{created: {Tokens: []parse.Token{
		{Address: []byte("M"), Created: true, Decimals: &six, TotalSupply: "1000000000000000"},
		{Address: []byte("N"), Created: true, Decimals: &six}, {Address: []byte("U"), Created: true, Decimals: &six}, {Address: []byte("X"), Created: true, Decimals: &six},
	}, Pools: []parse.Pool{
		{ID: []byte("A"), Token: []byte("M"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active", ReserveToken: "1000000", ReserveQuote: "100000000000"},
		{ID: []byte("B"), Token: []byte("M"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active", ReserveToken: "1000000", ReserveQuote: "1000000000"},
		{ID: []byte("U"), Token: []byte("U"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"},
		{ID: []byte("X"), Token: []byte("X"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"},
	}}}
	for i, x := range []struct {
		second                    int
		token, pool, quote, price string
	}{
		{5, "M", "A", "10000000", "0.01"}, {6, "X", "X", "10000000", "0.01"},
		{125, "M", "A", "20000000", "0.02"}, {126, "U", "U", "20000000", "0.02"},
		{185, "M", "A", "30000000", "0.03"}, {305, "M", "B", "40000000", "0.04"},
		{365, "M", "B", "50000000", "0.05"}, {370, "M", "A", "90000000", "0.09"},
	} {
		tr := windowTrade(i+1, x.pool, x.quote, x.price, "w")
		tr.Token = []byte(x.token)
		f[windowStart.Add(time.Duration(x.second)*time.Second)] = parse.Facts{Trades: []parse.Trade{tr}}
	}
	seedWindowBlocks(t, s, "solana", created, windowStart.Add(10*time.Minute), f)
	if err := s.SaveMainPools(ctx, "solana", []store.MainPoolChange{{Token: []byte("M"), Pool: []byte("A"), Reason: "liquidity"}, {Token: []byte("U"), Pool: []byte("U"), Reason: "liquidity"}, {Token: []byte("X"), Pool: []byte("X"), Reason: "liquidity"}}, created); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMainPools(ctx, "solana", []store.MainPoolChange{{Token: []byte("M"), From: []byte("A"), Pool: []byte("B"), Reason: "liquidity"}}, windowStart.Add(270*time.Second)); err != nil {
		t.Fatal(err)
	}
	runWindowCompute(t, s, windowStart.Add(10*time.Minute))
	return s
}

func TestTokenCandlesAvailabilityAndHistory(t *testing.T) {
	ctx := context.Background()
	s := availabilityCandleFixture(t)
	now := windowStart.Add(10 * time.Minute)
	set, err := s.TokenCandles(ctx, "solana", []byte("M"), "1m", "price", false, windowStart, windowStart.Add(7*time.Minute), 10, now)
	if err != nil {
		t.Fatal(err)
	}
	wantGaps := [][2]time.Time{{windowStart.Add(2 * time.Minute), windowStart.Add(4 * time.Minute)}, {windowStart.Add(5 * time.Minute), windowStart.Add(6 * time.Minute)}}
	if !reflect.DeepEqual(set.Gaps, wantGaps) {
		t.Errorf("gaps=%v; want only consecutive main-pool unpriced buckets %v", set.Gaps, wantGaps)
	}
	if len(set.Switches) != 1 {
		t.Errorf("switches=%+v; want one A>B at +270s", set.Switches)
	} else {
		sw := set.Switches[0]
		if !sw.T.Equal(windowStart.Add(270*time.Second)) || string(sw.From) != "A" || string(sw.To) != "B" || sw.Reason != "liquidity" || sw.Seq <= 0 {
			t.Errorf("switch=%+v; want A>B liquidity at +270s with positive seq", sw)
		}
	}
	if len(set.Bars) != 2 || !set.Complete || set.EmptyReason != "" {
		t.Errorf("priced bars=%+v; want two with complete coverage", set)
	}
	for _, tc := range []struct {
		token, reason string
		from, to      time.Time
	}{
		{"N", "no_main_pool", windowStart, now},
		{"U", "no_price", windowStart, now},
		{"M", "no_trades", windowStart.Add(8 * time.Minute), now},
	} {
		got, err := s.TokenCandles(ctx, "solana", []byte(tc.token), "1m", "price", false, tc.from, tc.to, 10, now)
		if err != nil {
			t.Fatal(err)
		}
		if got.EmptyReason != tc.reason || len(got.Bars) != 0 {
			t.Errorf("%s empty=%+v; want reason %s and no bars", tc.token, got, tc.reason)
		}
	}
	// Rule 8: price data exists, but without current supply the requested
	// market-cap OHLC is NULL and its availability reason is no_price.
	unknown, err := s.TokenCandles(ctx, "solana", []byte("X"), "1m", "market_cap", false, windowStart, now, 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.EmptyReason != "no_price" || len(unknown.Bars) != 1 {
		t.Errorf("unknown-supply set=%+v; want one NULL market-cap bar, no_price", unknown)
	} else {
		b := unknown.Bars[0]
		if b.O != nil || b.H != nil || b.L != nil || b.C != nil {
			t.Errorf("unknown supply OHLC=%+v; want all NULL", b)
		}
	}
}

func TestTokenCandlesPartialCoverage(t *testing.T) {
	ctx := context.Background()
	full := tradeCandleFixture(t, false)
	partial := tradeCandleFixture(t, true)
	now := windowStart.Add(45 * time.Second)
	a, err := full.TokenCandles(ctx, "solana", []byte("M"), "30s", "price", false, windowStart, windowStart.Add(time.Minute), 10, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := partial.TokenCandles(ctx, "solana", []byte("M"), "30s", "price", false, windowStart, windowStart.Add(time.Minute), 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Complete || b.Complete || !reflect.DeepEqual(a.Bars, b.Bars) || len(b.Bars) != 2 {
		t.Errorf("full=%+v partial=%+v; want identical observed bars and true/false coverage", a, b)
	}
	// Frozen store boundary supplies Complete/Closed. H2 maps both bars to
	// observed_items for partial coverage, including the already closed bar.
	for _, bar := range b.Bars {
		if b.Complete && bar.Closed {
			t.Error("partial bar eligible for complete items")
		}
	}
}

func TestWindowSumsMatchRawTrades(t *testing.T) {
	ctx := context.Background()
	s, c := openWithConn(t)
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: []byte("wsol"), Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	var prices []store.QuotePrice
	for i := -60; i <= 6; i++ {
		prices = append(prices, store.QuotePrice{Symbol: "SOL", Minute: windowStart.Add(time.Duration(i) * time.Minute), USD: "100", Source: "binance"})
	}
	if err := s.SaveQuotePrices(ctx, prices); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	created := windowStart.Add(-time.Hour)
	facts := map[time.Time]parse.Facts{created: {Tokens: []parse.Token{{Address: []byte("M"), Created: true, Decimals: &six}}, Pools: []parse.Pool{
		{ID: []byte("A"), Token: []byte("M"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"},
		{ID: []byte("B"), Token: []byte("M"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"},
	}}}
	rng := rand.New(rand.NewSource(17))
	for i := 0; i < 200; i++ {
		second := rng.Intn(420) - 60
		// Explicit samples exactly at both boundaries of the 5m and 1m windows.
		if i < 4 {
			second = []int{0, 300, 239, 240}[i]
		}
		pool := []string{"A", "B"}[rng.Intn(2)]
		units := rng.Intn(99) + 1
		tr := windowTrade(i+1, pool, strconv.Itoa(units*1000000), fmt.Sprintf("0.%03d", units), fmt.Sprintf("w%d", rng.Intn(9)))
		if rng.Intn(2) == 0 {
			tr.Side = "sell"
		}
		if i%13 == 0 {
			tr.Wallet = nil
			tr.Attribution = "ambiguous"
		}
		at := windowStart.Add(time.Duration(second) * time.Second)
		f := facts[at]
		f.Trades = append(f.Trades, tr)
		facts[at] = f
	}
	seedWindowBlocks(t, s, "solana", created, windowStart.Add(6*time.Minute), facts)
	runWindowCompute(t, s, windowStart.Add(6*time.Minute))
	asOf := windowStart.Add(5 * time.Minute)
	windows := []time.Duration{time.Minute, 5 * time.Minute, time.Hour}
	for _, native := range []bool{false, true} {
		got, err := s.TokenWindows(ctx, "solana", []byte("M"), asOf.Add(59*time.Second), windows, native)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(windows) {
			t.Fatalf("windows=%d; want %d", len(got), len(windows))
		}
		unit := "value_usd"
		if native {
			unit = "value_native"
		}
		for i, w := range windows {
			var trades, buys, sells, wallets int64
			var volume, buy, sell, net string
			// Independent oracle: read original facts, without pool_minute_stats or
			// the implementation's window query. Complete/all-priced is a precondition.
			query := fmt.Sprintf(`SELECT count(*),count(*)FILTER(WHERE side='buy'),count(*)FILTER(WHERE side='sell'),count(DISTINCT wallet),
    trim_scale(sum(%[1]s))::text,trim_scale(coalesce(sum(%[1]s)FILTER(WHERE side='buy'),0))::text,
    trim_scale(coalesce(sum(%[1]s)FILTER(WHERE side='sell'),0))::text,
    trim_scale(sum(CASE WHEN side='buy' THEN %[1]s ELSE -%[1]s END))::text
    FROM trades WHERE chain='solana' AND token_address='M' AND block_time>=$1 AND block_time<$2`, unit)
			if err := c.QueryRow(ctx, query, asOf.Add(-w), asOf).Scan(&trades, &buys, &sells, &wallets, &volume, &buy, &sell, &net); err != nil {
				t.Fatal(err)
			}
			v := got[i]
			if !v.Complete || v.Trades != trades || v.Buys != buys || v.Sells != sells || v.Traders == nil || *v.Traders != wallets ||
				decimalValue(v.Volume) != volume || decimalValue(v.BuyVolume) != buy || decimalValue(v.SellVolume) != sell || decimalValue(v.NetInflow) != net {
				t.Errorf("native=%t window=%s got=%+v amounts=%s/%s/%s/%s; raw counts=%d/%d/%d wallets=%d amounts=%s/%s/%s/%s", native, w, v, decimalValue(v.Volume), decimalValue(v.BuyVolume), decimalValue(v.SellVolume), decimalValue(v.NetInflow), trades, buys, sells, wallets, volume, buy, sell, net)
			}
		}
	}
}

func TestTokenWindowsKeepsTinyPriceChanges(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: []byte("wsol"), Symbol: "SOL", Kind: "native", Decimals: 9}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: windowStart.Add(-time.Minute), USD: "100", Source: "binance"}, {Symbol: "SOL", Minute: windowStart.Add(time.Minute), USD: "100", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	six := int16(6)
	f := map[time.Time]parse.Facts{}
	for i, x := range []struct {
		at           time.Time
		quote, price string
	}{{windowStart.Add(-time.Minute), "10000000", "1e-52"}, {windowStart.Add(time.Minute), "20000000", "2e-52"}} {
		tr := windowTrade(i+1, "A", x.quote, x.price, "w")
		// 1e50 tokens for 0.01/0.02 SOL: USD price 1e-50/2e-50.
		tr.TokenAmount = "1" + strings.Repeat("0", 56)
		tr.ValueNative = []string{"0.01", "0.02"}[i]
		facts := parse.Facts{Trades: []parse.Trade{tr}}
		if i == 0 {
			facts.Tokens = []parse.Token{{Address: []byte("M"), Created: true, Decimals: &six}}
			facts.Pools = []parse.Pool{{ID: []byte("A"), Token: []byte("M"), Quote: []byte("wsol"), Protocol: "pumpswap", Dex: "pumpswap", Created: true, Status: "active"}}
		}
		f[x.at] = facts
	}
	seedWindowBlocks(t, s, "solana", windowStart.Add(-time.Minute), windowStart.Add(5*time.Minute), f)
	runWindowCompute(t, s, windowStart.Add(5*time.Minute))
	for _, native := range []bool{false, true} {
		got, err := s.TokenWindows(ctx, "solana", []byte("M"), windowStart.Add(5*time.Minute), []time.Duration{5 * time.Minute}, native)
		if err != nil {
			t.Fatal(err)
		}
		// Independent scale-free arithmetic: (2e-50 - 1e-50)/1e-50*100 = 100;
		// native prices differ by the same factor of two.
		if len(got) != 1 {
			t.Fatalf("windows=%d; want one", len(got))
		}
		if decimalValue(got[0].PriceChangePct) != "100" {
			t.Errorf("native=%t tiny-price change=%s; want 100", native, decimalValue(got[0].PriceChangePct))
		}
	}
}
