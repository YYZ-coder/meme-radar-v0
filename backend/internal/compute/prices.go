// Package compute derives prices, market state and statistics from the
// facts parse writes (docs/engineering/07-compute.md).
package compute

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/meme-radar/radar/internal/prices"
	"github.com/meme-radar/radar/internal/store"
)

// ChainNative names a chain's native coin symbol for USD prices.
type ChainNative struct{ Chain, Symbol string }

// retryGap is how long a minute the exchange had no candle for is left alone.
const retryGap = 10 * time.Minute

// Prices records native-coin USD prices per minute: the live price for the
// current minute (its last save is the minute close) and exchange 1m closes
// for earlier minutes that have unpriced facts.
type Prices struct {
	store    *store.Store
	src      prices.Source
	chains   []ChainNative
	lookback time.Duration
	tried    map[string]time.Time // symbol+minute -> when a candle was asked for
}

func NewPrices(s *store.Store, src prices.Source, chains []ChainNative, lookback time.Duration) *Prices {
	return &Prices{store: s, src: src, chains: chains, lookback: lookback, tried: map[string]time.Time{}}
}

// Step saves the live prices for now's minute, then fills missing minutes.
func (p *Prices) Step(ctx context.Context, now time.Time) error {
	current := now.UTC().Truncate(time.Minute)
	var symbols []string
	for _, c := range p.chains {
		if !slices.Contains(symbols, c.Symbol) {
			symbols = append(symbols, c.Symbol)
		}
	}
	live, err := p.src.Latest(ctx, symbols)
	if err != nil {
		return fmt.Errorf("compute: live prices: %w", err)
	}
	var rows []store.QuotePrice
	for _, s := range symbols {
		rows = append(rows, store.QuotePrice{Symbol: s, Minute: current, USD: live[s].USD, Source: live[s].Source})
	}
	if err := p.store.SaveQuotePrices(ctx, rows); err != nil {
		return err
	}
	for k, at := range p.tried {
		if now.Sub(at) >= retryGap {
			delete(p.tried, k)
		}
	}
	for _, c := range p.chains {
		missing, err := p.store.MissingPriceMinutes(ctx, c.Chain, c.Symbol, now.Add(-p.lookback))
		if err != nil {
			return err
		}
		var todo []time.Time
		for _, m := range missing {
			if m.Before(current) && p.tried[c.Symbol+m.String()].IsZero() {
				todo = append(todo, m)
			}
		}
		for _, r := range runs(todo) {
			got, err := p.src.Minutes(ctx, c.Symbol, r[0], r[1])
			if err != nil {
				return fmt.Errorf("compute: %s minute prices: %w", c.Symbol, err)
			}
			for m := r[0]; !m.After(r[1]); m = m.Add(time.Minute) {
				p.tried[c.Symbol+m.String()] = now
			}
			rows = rows[:0]
			for _, q := range got {
				if !q.T.Before(r[0]) && !q.T.After(r[1]) {
					rows = append(rows, store.QuotePrice{Symbol: c.Symbol, Minute: q.T, USD: q.USD, Source: q.Source})
				}
			}
			if err := p.store.SaveQuotePrices(ctx, rows); err != nil {
				return err
			}
		}
	}
	return nil
}

// runs groups ascending minutes into contiguous [first, last] ranges.
func runs(ms []time.Time) [][2]time.Time {
	var out [][2]time.Time
	for _, m := range ms {
		if n := len(out); n > 0 && m.Equal(out[n-1][1].Add(time.Minute)) {
			out[n-1][1] = m
			continue
		}
		out = append(out, [2]time.Time{m, m})
	}
	return out
}
