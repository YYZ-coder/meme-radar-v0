package pgapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/store"
)

func (b *Backend) currency(chain api.Chain, native bool) string {
	if !native {
		return "USD"
	}
	for _, c := range b.cfg.Chains {
		if c.Name == string(chain) {
			return c.Native
		}
	}
	return map[api.Chain]string{api.Solana: "SOL", api.Base: "ETH", api.BSC: "BNB"}[chain]
}

// Candles preserves H1's price evidence even when token decimals are unknown.
func (b *Backend) Candles(ctx context.Context, chain api.Chain, address string, q api.CandleQuery) (api.Candles, error) {
	token, _, err := b.checkedToken(ctx, chain, address, q.Metric == "market_cap")
	if err != nil {
		return api.Candles{}, err
	}
	step := map[string]time.Duration{"1s": time.Second, "30s": 30 * time.Second, "1m": time.Minute, "5m": 5 * time.Minute, "1h": time.Hour, "1d": 24 * time.Hour}[q.Interval]
	if step == 0 {
		return api.Candles{}, api.Errorf(http.StatusBadRequest, "invalid_param", "interval", "unsupported interval")
	}
	now := b.now().UTC()
	to := now.Truncate(step)
	if q.To != nil {
		to = *unixTime(q.To)
	}
	from := to.Add(-time.Duration(q.Limit) * step)
	if q.From != nil {
		from = *unixTime(q.From)
	}
	set, err := b.st.TokenCandles(ctx, string(chain), token, q.Interval, q.Metric, q.Native, from, to, q.Limit, now)
	if errors.Is(err, store.ErrRangeTooLong) {
		return api.Candles{}, api.Errorf(http.StatusBadRequest, "invalid_range", "from", "sub-minute candles are limited to the last 24 hours")
	}
	if err != nil {
		return api.Candles{}, err
	}
	out := api.Candles{Interval: q.Interval, Metric: q.Metric, Currency: b.currency(chain, q.Native), AsOf: to.Format("2006-01-02T15:04:05.000Z"), Coverage: "partial",
		Items: []api.Candle{}, ObservedItems: []api.ObservedCandle{}, Gaps: []api.CandleGap{}, PoolSwitches: []api.PoolSwitch{}}
	if set.Complete {
		out.Coverage = "complete"
	}
	if set.EmptyReason != "" {
		out.EmptyReason = &set.EmptyReason
	}
	for _, bar := range set.Bars {
		if bar.O == nil || bar.H == nil || bar.L == nil || bar.C == nil {
			continue
		}
		if set.Complete && bar.Closed && bar.V != nil {
			out.Items = append(out.Items, api.Candle{T: bar.T.Unix(), O: *bar.O, H: *bar.H, L: *bar.L, C: *bar.C, V: *bar.V})
		} else {
			out.ObservedItems = append(out.ObservedItems, api.ObservedCandle{T: bar.T.Unix(), O: *bar.O, H: *bar.H, L: *bar.L, C: *bar.C, V: bar.V, Closed: bar.Closed})
		}
	}
	for _, g := range set.Gaps {
		out.Gaps = append(out.Gaps, api.CandleGap{From: g[0].Unix(), To: g[1].Unix(), Reason: "price_unavailable"})
	}
	for _, sw := range set.Switches {
		var from *string
		if sw.From != nil {
			a := displayAddress(string(chain), sw.From)
			from = &a
		}
		out.PoolSwitches = append(out.PoolSwitches, api.PoolSwitch{T: sw.T.Unix(), FromPool: from, ToPool: displayAddress(string(chain), sw.To), Reason: sw.Reason})
	}
	return out, nil
}
