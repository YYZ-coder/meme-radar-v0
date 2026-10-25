package compute

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/meme-radar/radar/internal/lists"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/prices"
	"github.com/meme-radar/radar/internal/store"
)

// CycleConfig wires one compute pass.
type CycleConfig struct {
	Store    *store.Store
	Prices   prices.Source
	Chains   []ChainNative
	V3       []*V3Liquidity
	Lookback time.Duration // how far back unpriced facts are priced
	Log      *slog.Logger  // optional: logs steps slower than a second
	// FullEvery is how often a pass re-chooses main pools and refreshes all
	// pool liquidity; passes in between only reprice tokens with new trades.
	FullEvery time.Duration
}

// Cycle runs every compute step in dependency order: native prices, fact
// values, pool liquidity, main pools and market state, minute stats and
// candles, ATH (full passes) and coverage.
type Cycle struct {
	lists     *lists.Worker
	prices    *Prices
	values    *Values
	smtrades  *SmTrades
	v3        []*V3Liquidity
	liquidity *Liquidity
	market    *Market
	stats     *Stats
	ath       *ATH
	coverage  *Coverage
	balances  *Holdings
	lookback  time.Duration
	log       *slog.Logger
	fullEvery time.Duration
	lastFull  time.Time
	store     *store.Store
	chains    []string
}

// backlog sets the backlog gauges of every chain from the database.
func (c *Cycle) backlog(ctx context.Context, now time.Time) error {
	for _, ch := range c.chains {
		b, err := c.store.Backlog(ctx, ch, now)
		if err != nil {
			return err
		}
		rows, err := c.store.BalanceRows(ctx, ch)
		if err != nil {
			return err
		}
		metrics.TokenBalancesRows.WithLabelValues(ch).Set(float64(rows))
		metrics.ForwardLag.WithLabelValues(ch).Set(float64(b.ForwardLag))
		metrics.GapBlocks.WithLabelValues(ch).Set(float64(b.GapBlocks))
		metrics.UnparsedBlocks.WithLabelValues(ch).Set(float64(b.Unparsed))
		metrics.Trades24h.WithLabelValues(ch).Set(float64(b.Trades24h))
		metrics.UnpricedTrades.WithLabelValues(ch).Set(float64(b.Unpriced24h))
	}
	heads, err := c.store.InflowHeads(ctx)
	if err != nil {
		return err
	}
	// Age is unbounded until the first head exists, so the same stale alert
	// also covers chains/windows whose derived facts never become ready.
	for _, chain := range c.chains {
		for _, window := range []string{"1m", "5m", "1h", "4h", "24h"} {
			age := math.Inf(1)
			for _, h := range heads {
				if h.Chain == chain && h.Window == window {
					age = max(0, now.Sub(h.AsOf).Seconds())
					break
				}
			}
			metrics.ProjectionAgeSeconds.WithLabelValues(chain, window).Set(age)
		}
	}
	queued, oldest, err := c.store.ListJobBacklog(ctx)
	if err != nil {
		return err
	}
	age := 0.0
	if oldest != nil {
		age = max(0, now.Sub(*oldest).Seconds())
	}
	metrics.JobsQueued.WithLabelValues("list_build").Set(float64(queued))
	metrics.JobsOldestQueuedSeconds.WithLabelValues("list_build").Set(age)
	return nil
}

func NewCycle(c CycleConfig) *Cycle {
	var names []string
	for _, ch := range c.Chains {
		names = append(names, ch.Chain)
	}
	holdings := NewBalances(c.Store, names)
	holdings.log = c.Log
	return &Cycle{
		lists:  lists.New(c.Store, c.Log),
		prices: NewPrices(c.Store, c.Prices, c.Chains, c.Lookback), values: NewValues(c.Store, c.Chains), smtrades: NewSmTrades(c.Store, names), v3: c.V3,
		liquidity: NewLiquidity(c.Store, c.Chains), market: NewMarket(c.Store, c.Chains),
		stats: NewStats(c.Store, names), balances: holdings, ath: NewATH(c.Store, names), coverage: NewCoverage(c.Store, names), lookback: c.Lookback, log: c.Log, fullEvery: c.FullEvery,
		store: c.Store, chains: names,
	}
}

// Run makes one pass at now: a full pass (the first, then every FullEvery)
// also samples V3 balances, prices all pool liquidity, re-chooses main
// pools and rebuilds coverage. A failing price source or balance lookup does
// not stop the later steps (they keep using the prices already stored); all
// errors are returned joined.
func (c *Cycle) Run(ctx context.Context, now time.Time) error {
	var errs []error
	full := c.lastFull.IsZero() || now.Sub(c.lastFull) >= c.fullEvery
	type step struct {
		name string
		run  func() error
	}
	steps := []step{
		{"list_build", func() error { return c.lists.Step(ctx, now) }},
		{"prices", func() error { return c.prices.Step(ctx, now) }},
		{"values", func() error { return c.values.Step(ctx, now.Add(-c.lookback)) }},
		{"smtrades", func() error { return c.smtrades.Step(ctx, now) }},
	}
	if full {
		for _, v := range c.v3 {
			steps = append(steps, step{"v3", func() error { return v.Step(ctx, now) }})
		}
		steps = append(steps, step{"liquidity", func() error { return c.liquidity.Step(ctx, now) }})
	}
	steps = append(steps,
		step{"market", func() error { return c.market.Step(ctx, now, full) }},
		step{"stats", func() error { return c.stats.Step(ctx, now) }},
	)
	if full {
		steps = append(steps, step{"ath", func() error { return c.ath.Step(ctx) }}, step{"coverage", func() error { return c.coverage.Step(ctx) }})
		c.lastFull = now
	}
	steps = append(steps, step{"balances", func() error { return c.balances.Step(ctx, now) }})
	if full {
		steps = append(steps, step{"backlog", func() error { return c.backlog(ctx, now) }})
	}
	for _, step := range steps {
		start := time.Now()
		if err := step.run(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			metrics.ComputeStepErrors.WithLabelValues(step.name).Inc()
			errs = append(errs, err)
		}
		took := time.Since(start)
		metrics.ComputeStepSeconds.WithLabelValues(step.name).Observe(took.Seconds())
		if c.log != nil && took > time.Second {
			c.log.Info("slow compute step", "step", step.name, "took", took.Round(time.Millisecond).String())
		}
	}
	return errors.Join(errs...)
}
