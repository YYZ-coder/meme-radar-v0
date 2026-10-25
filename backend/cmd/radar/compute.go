package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"time"

	"golang.org/x/time/rate"

	"github.com/meme-radar/radar/internal/chaininfo"
	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/prices"
	"github.com/meme-radar/radar/internal/store"
)

const computeUsage = "usage: radar compute [--config=FILE] [--interval=10s] (RADAR_DATABASE_URL required; price sources and RPCs from the config file)"

// computeLookback bounds how far back unpriced facts and missing minute
// prices are filled: the raw-block retention (D143).
const computeLookback = 7 * 24 * time.Hour

type computeCmd struct {
	dbURL    string
	interval time.Duration
	chains   []config.Chain
	prices   config.Prices
	inflow   config.Compute
}

func (c computeCmd) natives() []compute.ChainNative {
	var out []compute.ChainNative
	for _, ch := range c.chains {
		out = append(out, compute.ChainNative{Chain: ch.Name, Symbol: ch.Native})
	}
	return out
}

func parseCompute(args []string, getenv func(string) string) (computeCmd, error) {
	if len(args) == 0 || args[0] != "compute" {
		return computeCmd{}, errors.New(computeUsage)
	}
	fs := flag.NewFlagSet("compute", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var c computeCmd
	fs.DurationVar(&c.interval, "interval", 10*time.Second, "time between compute passes")
	configPath := fs.String("config", "", "chain configuration file (default: RADAR_CONFIG, else built-in)")
	if err := fs.Parse(args[1:]); err != nil {
		return computeCmd{}, fmt.Errorf("%w\n%s", err, computeUsage)
	}
	if c.interval <= 0 {
		return computeCmd{}, errors.New("--interval must be positive")
	}
	cfg, err := loadConfig(*configPath, getenv)
	if err != nil {
		return computeCmd{}, err
	}
	c.chains, c.prices, c.inflow = cfg.Chains, cfg.Prices, cfg.Compute
	if c.dbURL = getenv("RADAR_DATABASE_URL"); c.dbURL == "" {
		return computeCmd{}, errors.New("RADAR_DATABASE_URL is required")
	}
	return c, nil
}

// decimalsFunc and balancesFunc adapt chaininfo lookups to compute.
type decimalsFunc func(ctx context.Context, asset []byte) (int16, error)

func (f decimalsFunc) Decimals(ctx context.Context, asset []byte) (int16, error) {
	return f(ctx, asset)
}

type balancesFunc func(ctx context.Context, token, holder []byte) (*big.Int, error)

func (f balancesFunc) BalanceOf(ctx context.Context, token, holder []byte) (*big.Int, error) {
	return f(ctx, token, holder)
}

func runCompute(ctx context.Context, c computeCmd, log *slog.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	st, err := store.Open(ctx, c.dbURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	unlock, err := st.LockChain(ctx, "compute")
	if err != nil {
		return err
	}
	defer unlock()
	client := &http.Client{Timeout: 30 * time.Second}
	var v3 []*compute.V3Liquidity
	for _, ch := range c.chains {
		if err := st.EnsureChain(ctx, ch.Name); err != nil {
			return err
		}
		url := ch.LookupRPCURL
		if url == "" {
			url = ch.Ingest.RPCURL
		}
		rpc := jsonrpc.New(url, client, rate.NewLimiter(rate.Limit(ch.LookupRPS), 2), nil)
		var look compute.Decimals
		if ch.Family == "solana" {
			look = decimalsFunc(chaininfo.NewSolana(rpc).MintDecimals)
		} else {
			info := chaininfo.New(rpc, evm.Address{})
			cached := chaininfo.NewCached(ch.Name, st, info)
			look = decimalsFunc(func(ctx context.Context, a []byte) (int16, error) {
				m, err := cached.ERC20(ctx, evm.Address(a))
				return int16(m.Decimals), err
			})
			if _, ok := ch.Protocol("pancake_v3"); ok {
				v3 = append(v3, compute.NewV3Liquidity(st, ch.Name, []string{"pancake_v3"}, balancesFunc(func(ctx context.Context, token, holder []byte) (*big.Int, error) {
					return info.BalanceOf(ctx, evm.Address(token), evm.Address(holder))
				})))
			}
		}
		qs, err := compute.QuoteAssets(ctx, ch, look)
		if err != nil {
			return err
		}
		if err := st.SaveQuoteAssets(ctx, qs); err != nil {
			return err
		}
	}
	src, err := prices.FromConfig(c.prices, client, os.Getenv)
	if err != nil {
		return err
	}
	cycle := compute.NewCycle(compute.CycleConfig{Store: st, Prices: src, Chains: c.natives(), V3: v3, Lookback: computeLookback, Log: log, FullEvery: time.Minute})
	// The projection writer owns its pool; a slow serial pass cannot occupy
	// its connection or dictate the independent D153 schedules.
	projectionStore, err := store.Open(ctx, c.dbURL)
	if err != nil {
		return err
	}
	defer projectionStore.Close()
	intervals := make(map[string]time.Duration, len(c.inflow.InflowIntervals))
	for window, interval := range c.inflow.InflowIntervals {
		intervals[window] = time.Duration(interval)
	}
	chains := make([]string, len(c.chains))
	for n, ch := range c.chains {
		chains[n] = ch.Name
	}
	projectionDone := make(chan error, 1)
	go func() {
		err := compute.NewInflow(projectionStore, chains, intervals, log).Run(ctx)
		projectionDone <- err
		cancel()
	}()
	defer func() {
		cancel()
		if err := <-projectionDone; err != nil && !errors.Is(err, context.Canceled) {
			log.Error("inflow loop stopped", "err", err)
		}
	}()
	log.Info("compute started", "chains", len(c.chains), "interval", c.interval.String())
	tick := time.NewTicker(c.interval)
	defer tick.Stop()
	var pruned, reported time.Time
	for {
		start := time.Now()
		if err := cycle.Run(ctx, start); err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Warn("compute pass incomplete", "err", err)
		}
		if took := time.Since(start); took > c.interval || start.Sub(reported) >= time.Minute {
			log.Info("compute pass", "took", took.Round(time.Millisecond).String())
			reported = start
		}
		if start.Sub(pruned) >= time.Hour {
			if err := st.PruneCompute(ctx, start); err != nil && ctx.Err() == nil {
				log.Warn("compute prune failed", "err", err)
			}
			pruned = start
		}
		select {
		case <-ctx.Done():
		case <-tick.C:
			continue
		}
		break
	}
	log.Info("stopped", "role", "compute")
	return nil
}
