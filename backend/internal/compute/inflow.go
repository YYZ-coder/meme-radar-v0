package compute

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/store"
)

type inflowStore interface {
	RebuildInflow(context.Context, string, string, time.Time) (store.ProjectionBuild, error)
}

// Inflow owns the single projection writer and each window's refresh schedule.
// Its store must use a connection pool separate from the serial compute cycle.
type Inflow struct {
	store     inflowStore
	chains    []string
	intervals map[string]time.Duration
	log       *slog.Logger
}

// NewInflow creates an independent projection loop; intervals use D153 keys.
func NewInflow(s inflowStore, chains []string, intervals map[string]time.Duration, log *slog.Logger) *Inflow {
	if log != nil {
		log = log.With("role", "compute")
	}
	schedule := make(map[string]time.Duration, len(intervals))
	for window, interval := range intervals {
		schedule[window] = interval
	}
	return &Inflow{store: s, chains: append([]string(nil), chains...), intervals: schedule, log: log}
}

// Run publishes due chain/windows without overlapping writes, until cancelled.
func (i *Inflow) Run(ctx context.Context) error {
	type job struct {
		chain, window string
		interval      time.Duration
		next          time.Time
	}
	started := time.Now()
	var jobs []job
	for _, window := range []string{"1m", "5m", "1h", "4h", "24h"} {
		interval := i.intervals[window]
		if interval <= 0 {
			return fmt.Errorf("compute: inflow interval %s must be positive", window)
		}
		for _, chain := range i.chains {
			jobs = append(jobs, job{chain, window, interval, started})
		}
	}
	if len(jobs) == 0 {
		<-ctx.Done()
		return ctx.Err()
	}
	// Spread all starts across one shortest refresh interval. With D153's
	// 5s/15s periods, the distinct phases also remain distinct on later ticks.
	shortest := jobs[0].interval
	for _, j := range jobs[1:] {
		shortest = min(shortest, j.interval)
	}
	for n := range jobs {
		jobs[n].next = started.Add(shortest * time.Duration(n) / time.Duration(len(jobs)))
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		next := 0
		for n := range jobs {
			if jobs[n].next.Before(jobs[next].next) {
				next = n
			}
		}
		j := &jobs[next]
		if wait := time.Until(j.next); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		at := time.Now()
		result, err := i.store.RebuildInflow(ctx, j.chain, j.window, at)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			metrics.ComputeStepErrors.WithLabelValues("inflow").Inc()
		}
		if err == nil && result.Rebuilt {
			metrics.InflowRebuildSeconds.WithLabelValues(j.chain, j.window).Observe(time.Since(at).Seconds())
			metrics.InflowRows.WithLabelValues(j.chain, j.window).Set(float64(result.Rows))
		}
		if i.log != nil {
			switch {
			case err != nil:
				i.log.Warn("inflow rebuild failed", "chain", j.chain, "window", j.window, "err", err)
			case result.SkipReason != "":
				i.log.Info("inflow publication waiting", "chain", j.chain, "window", j.window, "reason", result.SkipReason)
			case result.Rebuilt:
				i.log.Info("inflow published", "chain", j.chain, "window", j.window, "rows", result.Rows, "query", result.QueryTime, "fill", result.FillTime, "write", result.WriteTime)
			}
		}
		// Keep each schedule anchored to its own deadlines. A slow build coalesces
		// missed ticks instead of queueing overlapping versions or starving windows.
		now := time.Now()
		j.next = now.Add(j.interval - now.Sub(j.next)%j.interval)
	}
}
