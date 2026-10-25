package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/meme-radar/radar/internal/chaininfo"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/enrich"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/metadata"
	"github.com/meme-radar/radar/internal/store"
)

const enrichUsage = "usage: radar enrich [--config=FILE] [--interval=30s] (RADAR_DATABASE_URL required; RPCs from the config file)"

// enrichCmd runs supply enrichment (D150), then display metadata (H6).
type enrichCmd struct {
	dbURL    string
	interval time.Duration
	chains   []config.Chain
	metadata config.Metadata
}

func parseEnrich(args []string, getenv func(string) string) (enrichCmd, error) {
	if len(args) == 0 || args[0] != "enrich" {
		return enrichCmd{}, errors.New(enrichUsage)
	}
	fs := flag.NewFlagSet("enrich", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var c enrichCmd
	fs.DurationVar(&c.interval, "interval", 30*time.Second, "time between lookup passes per chain")
	configPath := fs.String("config", "", "chain configuration file (default: RADAR_CONFIG, else built-in)")
	if err := fs.Parse(args[1:]); err != nil {
		return enrichCmd{}, fmt.Errorf("%w\n%s", err, enrichUsage)
	}
	if c.interval <= 0 {
		return enrichCmd{}, errors.New("--interval must be positive")
	}
	cfg, err := loadConfig(*configPath, getenv)
	if err != nil {
		return enrichCmd{}, err
	}
	c.chains = cfg.Chains
	c.metadata = cfg.Metadata
	if c.dbURL = getenv("RADAR_DATABASE_URL"); c.dbURL == "" {
		return enrichCmd{}, errors.New("RADAR_DATABASE_URL is required")
	}
	return c, nil
}

type supplyFunc func(ctx context.Context, token []byte) (string, int16, error)

func (f supplyFunc) Supply(ctx context.Context, token []byte) (string, int16, error) {
	return f(ctx, token)
}

// enrichRPC shares the existing chain RPC budget between the two steps.
func enrichRPC(ch config.Chain) *jsonrpc.Client {
	url := ch.LookupRPCURL
	if url == "" {
		url = ch.Ingest.RPCURL
	}
	return jsonrpc.New(url, &http.Client{Timeout: 30 * time.Second}, rate.NewLimiter(rate.Limit(ch.LookupRPS), 2), nil)
}

// supplyLookup reads getTokenSupply on Solana, totalSupply/decimals on EVM.
func supplyLookup(ch config.Chain, st *store.Store, rpc *jsonrpc.Client) enrich.Supply {
	if ch.Family == "solana" {
		return supplyFunc(chaininfo.NewSolana(rpc).MintSupply)
	}
	info := chaininfo.New(rpc, evm.Address{})
	cached := chaininfo.NewCached(ch.Name, st, info)
	return supplyFunc(func(ctx context.Context, token []byte) (string, int16, error) {
		n, err := info.TotalSupply(ctx, evm.Address(token))
		if err != nil {
			return "", 0, err
		}
		m, err := cached.ERC20(ctx, evm.Address(token))
		if err != nil {
			return "", 0, err
		}
		return n.String(), int16(m.Decimals), nil
	})
}

func runEnrich(ctx context.Context, c enrichCmd, log *slog.Logger) error {
	st, err := store.Open(ctx, c.dbURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	unlock, err := st.LockChain(ctx, "enrich")
	if err != nil {
		return err
	}
	defer unlock()
	log.Info("enrich started", "chains", len(c.chains), "interval", c.interval.String())
	f := metadata.NewFetcher(metadata.Options{IPFSGateways: c.metadata.IPFSGateways, FourMemeAPIURL: c.metadata.FourMemeAPIURL,
		HostCooldown: time.Duration(c.metadata.HostCooldown), HostFailures: c.metadata.HostFailures,
		RPS: c.metadata.RPS, PerHostRPS: c.metadata.PerHostRPS, FourMemeRPS: c.metadata.FourMemeRPS})
	defer f.CloseIdleConnections()
	var wg sync.WaitGroup
	for _, ch := range c.chains {
		if err := st.EnsureChain(ctx, ch.Name); err != nil {
			return err
		}
		// Both steps share this chain's RPC budget; all chains share HTTP budgets.
		rpc := enrichRPC(ch)
		e := enrich.New(st, ch.Name, supplyLookup(ch, st, rpc))
		m := metadata.New(st, ch.Name, f, metadata.NewChainSource(ch.Family, rpc), log)
		wg.Add(1)
		go func() { // chains have separate RPC budgets
			defer wg.Done()
			tick := time.NewTicker(c.interval)
			defer tick.Stop()
			for {
				if err := e.Step(ctx, time.Now()); err != nil && ctx.Err() == nil {
					log.Warn("enrich pass incomplete", "chain", ch.Name, "err", err)
				}
				if err := m.Step(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
					log.Warn("metadata pass incomplete", "role", "enrich", "chain", ch.Name, "err", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		}()
	}
	wg.Wait()
	log.Info("stopped", "role", "enrich")
	return nil
}
