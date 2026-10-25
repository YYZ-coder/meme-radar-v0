package compute_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

// H4-8: literal expected closes cover leading NULLs, forward filling and the
// main-pool switch. The high off-main prices must never enter either series.
func TestInflowSparkline(t *testing.T) {
	for _, window := range []string{"1h", "5m"} {
		for _, seeded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/seed=%t", window, seeded), func(t *testing.T) {
				s, c := open(t)
				now := day.Add(17 * time.Hour)
				width, _ := time.ParseDuration(window)
				from := now.Add(-width)
				apply(t, s, "bsc", 1, from.Add(-time.Minute), h4Facts())
				type event struct {
					offset time.Duration
					pool   byte
					price  int
				}
				events := []event{{4 * time.Minute, 90, 2}, {7 * time.Minute, 90, 3}, {7*time.Minute + 40*time.Second, 90, 4}, {8 * time.Minute, 91, 99}, {31 * time.Minute, 91, 6}, {35 * time.Minute, 90, 222}, {59 * time.Minute, 91, 5}}
				switchAt := from.Add(30 * time.Minute)
				expected := []string{"NULL", "2", "4", "4", "4", "4", "4", "4", "4", "4", "6", "6", "6", "6", "6", "6", "6", "6", "6", "5"}
				if window == "5m" {
					events = []event{{16 * time.Second, 90, 2}, {31 * time.Second, 90, 3}, {150 * time.Second, 91, 4}, {151 * time.Second, 90, 99}, {299 * time.Second, 91, 5}}
					switchAt = from.Add(150 * time.Second)
					expected = []string{"NULL", "2", "3", "3", "3", "3", "3", "3", "3", "3", "4", "4", "4", "4", "4", "4", "4", "4", "4", "5"}
				}
				if seeded {
					events = append([]event{{-time.Second, 90, 7}}, events...)
					expected[0] = "7"
				}
				num := uint64(1)
				for _, e := range events {
					num++
					at := from.Add(e.offset)
					h4Quotes(t, s, at.Truncate(time.Minute))
					tr := h4Trade(1, 1, fmt.Sprint(e.price*1000000))
					tr.Pool = h4Key(e.pool)
					tr.PriceNative = fmt.Sprint(e.price)
					f := parse.Facts{Trades: []parse.Trade{tr}}
					if e.pool == 91 {
						f.Pools = []parse.Pool{{ID: h4Key(91), Token: h4Key(80), Quote: h4Key(99), QuoteSymbol: "USDC", Dex: "alt", Protocol: "test", Status: "active"}}
					}
					apply(t, s, "bsc", num, at, f)
				}
				num++
				apply(t, s, "bsc", num, now.Add(time.Second), parse.Facts{})
				if err := s.SaveMainPools(t.Context(), "bsc", []store.MainPoolChange{{Token: h4Key(80), Pool: h4Key(90), Reason: "liquidity"}}, from.Add(-time.Minute)); err != nil {
					t.Fatal(err)
				}
				if err := s.SaveMainPools(t.Context(), "bsc", []store.MainPoolChange{{Token: h4Key(80), Pool: h4Key(91), Reason: "liquidity"}}, switchAt); err != nil {
					t.Fatal(err)
				}
				for _, err := range []error{s.FillValues(t.Context(), "bsc", "BNB", from.Add(-time.Minute)), s.RefreshStats(t.Context(), "bsc", now), s.RefreshSmTrades(t.Context(), "bsc", now), s.RefreshCoverage(t.Context(), "bsc")} {
					if err != nil {
						t.Fatal(err)
					}
				}
				built, err := s.RebuildInflow(t.Context(), "bsc", window, now)
				if err != nil {
					t.Fatal(err)
				}
				var values, native []*string
				if err = c.QueryRow(t.Context(), `SELECT ARRAY(SELECT trim_scale(x)::text FROM unnest(sparkline)x),ARRAY(SELECT trim_scale(x)::text FROM unnest(sparkline_native)x) FROM inflow_rows WHERE projection_version=$1`, built.Version).Scan(&values, &native); err != nil {
					t.Fatal(err)
				}
				if len(values) != 20 || len(native) != 20 {
					t.Fatalf("sparkline lengths=%d/%d, want 20/20", len(values), len(native))
				}
				for i, want := range expected {
					checkH4Text(t, fmt.Sprintf("USD bucket %d", i), values[i], want)
					nw := map[string]string{"NULL": "NULL", "2": "0.02", "3": "0.03", "4": "0.04", "5": "0.05", "6": "0.06", "7": "0.07"}[want]
					checkH4Text(t, fmt.Sprintf("native bucket %d", i), native[i], nw)
				}
			})
		}
	}
}
