package compute_test

import (
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

// H4-7 uses the frozen inflow_rows persistence boundary. Counts and amounts
// are hand-calculated: all-pool 3 trades/6 USD, smart-money buys 3+2=5 USD.
func TestInflowRowsFromFacts(t *testing.T) {
	for _, partial := range []bool{false, true} {
		name := "complete"
		if partial {
			name = "partial"
		}
		t.Run(name, func(t *testing.T) {
			s, c := open(t)
			now := day.Add(16 * time.Hour)
			h4Members(t, s, now, true)
			old := now.Add(-10 * time.Minute)
			h4Quotes(t, s, old)
			h4Quotes(t, s, now)
			h4Quotes(t, s, now.Add(-time.Minute))
			f := h4Facts(h4Trade(1, 1, "1000000"))
			f.Tokens[0].TotalSupply = "1000000000"
			f.Tokens[0].Creator = h4Key(1)
			f.Tokens[0].LaunchPlatform = "four_meme"
			f.Pools[0].ReserveQuote = "100000000"
			f.Pools[0].ReserveToken = "1000000"
			six := int16(6)
			f.Tokens = append(f.Tokens, parse.Token{Address: h4Key(81), Symbol: "NO-TRADE", Created: true, Decimals: &six})
			f.Pools = append(f.Pools, parse.Pool{ID: h4Key(91), Token: h4Key(80), Quote: h4Key(99), QuoteSymbol: "USDC", Dex: "alt", Protocol: "test", Created: true, Status: "active", ReserveQuote: "50000000", ReserveToken: "1000000"})
			apply(t, s, "bsc", 1, old, f)
			buy := h4Trade(1, 1, "3000000")
			sell := h4Trade(2, 3, "1000000")
			sell.Side = "sell"
			other := h4Trade(3, 1, "2000000")
			other.Pool = h4Key(91)
			num := uint64(2)
			if partial {
				num = 3
			}
			at := now.Add(-time.Minute)
			apply(t, s, "bsc", num, at, parse.Facts{Trades: []parse.Trade{buy, sell, other}})
			apply(t, s, "bsc", num+1, now.Add(time.Second), parse.Facts{})
			for _, err := range []error{
				s.FillValues(t.Context(), "bsc", "BNB", old), s.SampleLiquidity(t.Context(), "bsc"), s.PriceLiquidity(t.Context(), "bsc", "BNB", now),
				s.SaveMainPools(t.Context(), "bsc", []store.MainPoolChange{{Token: h4Key(80), Pool: h4Key(90), Reason: "liquidity"}}, old),
				s.UpdateMarket(t.Context(), "bsc", now, false), s.RefreshStats(t.Context(), "bsc", now), s.RefreshSmTrades(t.Context(), "bsc", now), s.RefreshCoverage(t.Context(), "bsc"),
			} {
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, n := range []uint64{1, num, num + 1} {
				bt := now.Add(time.Second)
				if n == 1 {
					bt = old
				} else if n == num {
					bt = at
				}
				deltas := []parse.Balance{}
				if n == 1 {
					deltas = []parse.Balance{{Token: h4Key(80), Owner: h4Key(1), Delta: "10000000"}, {Token: h4Key(80), Owner: h4Key(2), Delta: "20000000"}, {Token: h4Key(80), Owner: h4Key(3), Delta: "30000000"}}
				}
				if err := s.ApplyBalances(t.Context(), []store.BalanceBlock{{Chain: "bsc", Num: n, Time: bt, Version: 1, Deltas: deltas}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.RefreshBalances(t.Context(), "bsc", now, 1); err != nil {
				t.Fatal(err)
			}
			built, err := s.RebuildInflow(t.Context(), "bsc", "5m", now)
			if err != nil {
				t.Fatal(err)
			}
			if got := one(t, c, `SELECT count(*)::text FROM inflow_rows WHERE projection_version=$1`, built.Version); got != "1" {
				t.Fatalf("published rows=%s, want only the 1 traded token", got)
			}
			got := one(t, c, `SELECT concat_ws('|',symbol,trim_scale(price_usd)::text,trim_scale(price_native)::text,trim_scale(market_cap_usd)::text,
    trim_scale(liquidity_usd)::text,observed_trades,observed_buys,observed_sells,age_seconds,array_to_string(dexes,','),holders,sm_holders,trim_scale(dev_holding_pct)::text)
    FROM inflow_rows WHERE projection_version=$1`, built.Version)
			if got != "H4|1|0.01|1000|300|3|2|1|600|alt,test|3|1|1" {
				t.Errorf("row metadata/current/observed=%s, want H4|1|0.01|1000|300|3|2|1|600|alt,test|3|1|1", got)
			}
			window := one(t, c, `SELECT concat_ws('|',coverage,coalesce(trades::text,'NULL'),coalesce(buys::text,'NULL'),coalesce(sells::text,'NULL'),
   coalesce(trim_scale(volume_usd)::text,'NULL'),coalesce(trim_scale(volume_native)::text,'NULL'),coalesce(trim_scale(net_inflow_usd)::text,'NULL'),
   coalesce(trim_scale(net_inflow_native)::text,'NULL'),coalesce(sm_trades::text,'NULL'),coalesce(trim_scale(price_change_pct)::text,'NULL')) FROM inflow_rows WHERE projection_version=$1`, built.Version)
			want := "complete|3|2|1|6|0.06|5|0.05|2|0"
			if partial {
				want = "partial|NULL|NULL|NULL|NULL|NULL|NULL|NULL|NULL|NULL"
			}
			if window != want {
				t.Errorf("window row=%s, want %s", window, want)
			}
			windows, err := s.TokenWindows(t.Context(), "bsc", h4Key(80), now, []time.Duration{5 * time.Minute}, false)
			if err != nil {
				t.Fatal(err)
			}
			if windows[0].Trades != 3 || windows[0].Volume == nil || *windows[0].Volume != "6" {
				t.Fatalf("unchanged H1 window=%+v, want observed 3/6", windows[0])
			}
		})
	}
}
