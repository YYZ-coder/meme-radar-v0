package pgapi_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
)

func TestPgCandles(t *testing.T) {
	f := detailFixture(t, false)
	h := f.handler()
	const route = "/api/v1/tokens/{chain}/{address}/candles"
	const target = "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061/candles"
	// Exact independent literals from the H1 worked example. Native chart
	// volume remains USD; market-cap OHLC is price times 1e9 whole tokens.
	for _, unit := range []struct{ metric, currency, echo, low, high string }{
		{"price", "usd", "USD", "50", "100"}, {"price", "native", "BNB", "0.5", "1"},
		{"market_cap", "usd", "USD", "50000000000", "100000000000"}, {"market_cap", "native", "BNB", "500000000", "1000000000"},
	} {
		for _, period := range []struct {
			interval                          string
			from, at                          time.Time
			volume                            string
			partial, closed, lowOpen, onlyLow bool
		}{
			{"1s", apiTime.Add(-5 * time.Minute), apiTime.Add(-5*time.Minute + 15*time.Second), "50", false, true, true, true},
			{"30s", apiTime.Add(-5 * time.Minute), apiTime.Add(-5 * time.Minute), "150", false, true, false, false},
			{"1m", apiTime.Add(-5 * time.Minute), apiTime.Add(-5 * time.Minute), "150", false, true, false, false},
			{"5m", apiTime.Add(-5 * time.Minute), apiTime.Add(-5 * time.Minute), "350", false, true, false, false},
			{"1h", apiTime.Add(-time.Hour), apiTime.Add(-time.Hour), "400", true, true, true, false},
			{"1d", time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC), "400", true, false, true, false},
		} {
			t.Run(period.interval+"/"+unit.metric+"/"+unit.currency, func(t *testing.T) {
				var got api.Candles
				query := fmt.Sprintf("?interval=%s&metric=%s&currency=%s&from=%d&to=%d&limit=1", period.interval, unit.metric, unit.currency, period.from.Unix(), apiTime.Unix())
				getPG(t, h, route, target+query, 200, &got)
				if got.Interval != period.interval || got.Metric != unit.metric || got.Currency != unit.echo || got.AsOf != "2026-10-23T10:00:00.000Z" || got.EmptyReason != nil || got.Items == nil || got.ObservedItems == nil || got.Gaps == nil || got.PoolSwitches == nil || len(got.Gaps) != 0 {
					t.Errorf("metadata=%+v", got)
				}
				var at int64
				var o, hi, lo, c, v string
				if period.partial {
					if got.Coverage != "partial" || len(got.Items) != 0 || len(got.ObservedItems) != 1 {
						t.Fatalf("partial bar=%+v", got)
					}
					b := got.ObservedItems[0]
					at, o, hi, lo, c = b.T, b.O, b.H, b.L, b.C
					if b.V == nil || b.Closed != period.closed {
						t.Fatalf("observed=%+v", b)
					}
					v = *b.V
					if len(got.PoolSwitches) != 1 || got.PoolSwitches[0].FromPool != nil || got.PoolSwitches[0].ToPool != "0x00000000000000000000000000000000000000a6" || got.PoolSwitches[0].Reason != "liquidity" {
						t.Errorf("switches=%+v", got.PoolSwitches)
					}
				} else {
					if got.Coverage != "complete" || len(got.Items) != 1 || len(got.ObservedItems) != 0 {
						t.Fatalf("closed bar=%+v", got)
					}
					b := got.Items[0]
					at, o, hi, lo, c, v = b.T, b.O, b.H, b.L, b.C, b.V
				}
				wantOpen, wantHigh := unit.high, unit.high
				if period.lowOpen {
					wantOpen = unit.low
				}
				if period.onlyLow {
					wantHigh = unit.low
				}
				if at != period.at.Unix() || o != wantOpen || hi != wantHigh || lo != unit.low || c != unit.low || v != period.volume {
					t.Errorf("bar=(%d %s %s %s %s %s), want=(%d %s %s %s %s %s)", at, o, hi, lo, c, v, period.at.Unix(), wantOpen, wantHigh, unit.low, unit.low, period.volume)
				}
			})
		}
	}
	for _, interval := range []string{"1s", "30s"} {
		var failure api.ErrorBody
		getPG(t, h, route, fmt.Sprintf("%s?interval=%s&from=%d&to=%d", target, interval, f.now.Add(-24*time.Hour-time.Second).Unix(), apiTime.Unix()), 400, &failure)
		if failure.Error.Code != "invalid_range" {
			t.Errorf("long range=%+v", failure)
		}
	}
}
