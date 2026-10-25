package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"github.com/meme-radar/radar/internal/chaininfo"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/parse/clanker"
	"github.com/meme-radar/radar/internal/parse/doppler"
	"github.com/meme-radar/radar/internal/parse/fourmeme"
	"github.com/meme-radar/radar/internal/parse/pancake"
	"github.com/meme-radar/radar/internal/parse/pump"
	"github.com/meme-radar/radar/internal/parser"
	"github.com/meme-radar/radar/internal/store"
)

const parseUsage = "usage: radar parse --chain=NAME [--config=FILE] [--batch=N] [--debug-addr=127.0.0.1:port] (RADAR_DATABASE_URL required)"

// implemented lists the protocol decoders per family; configured protocols
// without one are skipped with a warning.
var implemented = map[string][]string{
	"solana": {"pump_fun", "pumpswap"},
	"evm":    {"four_meme", "pancake_v2", "pancake_v3", "pancake_infinity_cl", "clanker_v4", "doppler"},
}

// dependencies are protocol types that only give addresses to decoders
// (uniswap_v4: the PoolManager for clanker_v4).
var dependencies = map[string]bool{"uniswap_v4": true}

// parseCmd is the parsed `radar parse` command line.
type parseCmd struct {
	chain, family, dbURL, debugAddr string
	lookupURL                       string
	batch                           int
	poll                            time.Duration
	decoders, skipped               []string
	ch                              config.Chain
}

func parseParse(args []string, getenv func(string) string) (parseCmd, error) {
	if len(args) == 0 || args[0] != "parse" {
		return parseCmd{}, errors.New(parseUsage)
	}
	fs := flag.NewFlagSet("parse", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c := parseCmd{poll: time.Second}
	fs.StringVar(&c.chain, "chain", "", "chain to parse")
	fs.IntVar(&c.batch, "batch", 50, "blocks per step (one transaction)")
	fs.StringVar(&c.debugAddr, "debug-addr", "", "loopback address for pprof; empty disables it")
	configPath := fs.String("config", "", "chain configuration file (default: RADAR_CONFIG, else built-in)")
	if err := fs.Parse(args[1:]); err != nil {
		return parseCmd{}, fmt.Errorf("%w\n%s", err, parseUsage)
	}
	cfg, err := loadConfig(*configPath, getenv)
	if err != nil {
		return parseCmd{}, err
	}
	ch, ok := cfg.Chain(c.chain)
	if !ok {
		return parseCmd{}, fmt.Errorf("--chain=%q is not in the configuration\n%s", c.chain, parseUsage)
	}
	c.ch, c.family = ch, ch.Family
	c.lookupURL = ch.LookupRPCURL
	if c.lookupURL == "" {
		c.lookupURL = ch.Ingest.RPCURL
	}
	for _, p := range ch.Protocols {
		if dependencies[p.Type] {
			continue
		}
		if slices.Contains(implemented[ch.Family], p.Type) {
			c.decoders = append(c.decoders, p.Type)
		} else {
			c.skipped = append(c.skipped, p.Type)
		}
	}
	if len(c.decoders) == 0 {
		return parseCmd{}, fmt.Errorf("chain %s has no implemented decoders among %v", c.chain, c.skipped)
	}
	if c.batch < 1 {
		return parseCmd{}, errors.New("--batch must be positive")
	}
	if err := checkLoopback(c.debugAddr); err != nil {
		return parseCmd{}, err
	}
	if c.dbURL = getenv("RADAR_DATABASE_URL"); c.dbURL == "" {
		return parseCmd{}, errors.New("RADAR_DATABASE_URL is required")
	}
	return c, nil
}

// blockDecoder assembles the chain's decoders. The version is the sum of
// the decoder versions, so enabling or upgrading a decoder reparses.
func blockDecoder(c parseCmd, st *store.Store) (parser.BlockDecoder, int, error) {
	if c.family == "solana" {
		return parser.Solana, pump.Version, nil // one bundle: pump_fun + pumpswap
	}
	var decs []parser.TxDecoder
	version := 0
	// Tracked tokens and quote metadata come from one cached lookup, which
	// needs the four_meme protocol's TokenManagerHelper3.
	var helper3 evm.Address
	four, hasFour := c.ch.Protocol("four_meme")
	if hasFour {
		helper3 = evm.MustAddress(four.Addresses["helper3"])
	}
	rpc := jsonrpc.New(c.lookupURL, &http.Client{Timeout: 30 * time.Second}, rate.NewLimiter(rate.Limit(c.ch.LookupRPS), 4), nil)
	lookup := chaininfo.NewCached(c.chain, st, chaininfo.New(rpc, helper3))
	quotes := map[evm.Address]string{}
	for sym, a := range c.ch.QuoteAssets {
		if addr := evm.MustAddress(a); addr != (evm.Address{}) {
			quotes[addr] = sym
		}
	}
	for _, name := range c.decoders {
		p, _ := c.ch.Protocol(name)
		if strings.HasPrefix(name, "pancake_") && !hasFour {
			return nil, 0, fmt.Errorf("decoder %s tracks Four.meme tokens and needs the four_meme protocol on chain %s", name, c.chain)
		}
		switch name {
		case "four_meme":
			decs = append(decs, fourmeme.New(fourmeme.Config{
				ManagerV1: evm.MustAddress(p.Addresses["token_manager_v1"]), ManagerV2: evm.MustAddress(p.Addresses["token_manager_v2"]),
				WrappedNative: evm.MustAddress(c.ch.WrappedNative), NativeSymbol: c.ch.Native, QuoteSymbols: quotes,
			}, lookup))
			version += fourmeme.Version
		case "pancake_v2", "pancake_v3", "pancake_infinity_cl":
			cfg := pancake.Config{WrappedNative: evm.MustAddress(c.ch.WrappedNative), NativeSymbol: c.ch.Native, QuoteSymbols: quotes}
			switch name {
			case "pancake_v2":
				hash, err := hash32(p.Addresses["init_code_hash"])
				if err != nil {
					return nil, 0, fmt.Errorf("pancake_v2 init_code_hash: %w", err)
				}
				cfg.V2Factory, cfg.V2InitCodeHash = evm.MustAddress(p.Addresses["factory"]), hash
				decs = append(decs, pancake.NewV2(cfg, lookup))
			case "pancake_v3":
				hash, err := hash32(p.Addresses["init_code_hash"])
				if err != nil {
					return nil, 0, fmt.Errorf("pancake_v3 init_code_hash: %w", err)
				}
				cfg.V3Deployer, cfg.V3InitCodeHash = evm.MustAddress(p.Addresses["deployer"]), hash
				for _, fee := range p.Fees {
					cfg.V3Fees = append(cfg.V3Fees, uint32(fee))
				}
				decs = append(decs, pancake.NewV3(cfg, lookup))
			default:
				cfg.InfinityCLPoolManager = evm.MustAddress(p.Addresses["pool_manager"])
				decs = append(decs, pancake.NewInfinityCL(cfg, lookup))
			}
			version += pancake.Version
		case "clanker_v4":
			v4, ok := c.ch.Protocol("uniswap_v4")
			if !ok {
				return nil, 0, fmt.Errorf("clanker_v4 needs the uniswap_v4 protocol (PoolManager) on chain %s", c.chain)
			}
			var hooks []evm.Address
			for _, h := range p.Hooks {
				hooks = append(hooks, evm.MustAddress(h))
			}
			symbols := map[evm.Address]string{}
			for sym, a := range c.ch.QuoteAssets {
				symbols[evm.MustAddress(a)] = sym
			}
			decs = append(decs, clanker.New(clanker.Config{Factory: evm.MustAddress(p.Addresses["factory"]),
				PoolManager: evm.MustAddress(v4.Addresses["pool_manager"]), Hooks: hooks,
				WrappedNative: evm.MustAddress(c.ch.WrappedNative), NativeSymbol: c.ch.Native, QuoteSymbols: symbols}, lookup))
			version += clanker.Version
		case "doppler":
			v4, ok := c.ch.Protocol("uniswap_v4")
			if !ok {
				return nil, 0, fmt.Errorf("doppler needs the uniswap_v4 protocol (PoolManager) on chain %s", c.chain)
			}
			var hooks []evm.Address
			for _, h := range p.Hooks {
				hooks = append(hooks, evm.MustAddress(h))
			}
			symbols := map[evm.Address]string{}
			for sym, a := range c.ch.QuoteAssets {
				symbols[evm.MustAddress(a)] = sym
			}
			decs = append(decs, doppler.New(doppler.Config{Airlock: evm.MustAddress(p.Addresses["airlock"]),
				PoolManager: evm.MustAddress(v4.Addresses["pool_manager"]), Hooks: hooks,
				WrappedNative: evm.MustAddress(c.ch.WrappedNative), NativeSymbol: c.ch.Native, QuoteSymbols: symbols}, lookup))
			version += doppler.Version
		default:
			return nil, 0, fmt.Errorf("decoder %s is not wired", name)
		}
	}
	return parser.EVM(decs...), version, nil
}

func runParse(ctx context.Context, c parseCmd, log *slog.Logger) error {
	st, err := store.Open(ctx, c.dbURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	if err := st.EnsureChain(ctx, c.chain); err != nil {
		return err
	}
	// A lock separate from the chain's ingest lock.
	unlock, err := st.LockChain(ctx, "parse:"+c.chain)
	if err != nil {
		return err
	}
	defer unlock()
	if len(c.skipped) > 0 {
		log.Warn("protocols without a decoder are not parsed", "chain", c.chain, "protocols", c.skipped)
	}
	if c.debugAddr != "" {
		stop := serveDebug(c.debugAddr, log)
		defer stop()
	}
	decode, version, err := blockDecoder(c, st)
	if err != nil {
		return err
	}
	group, runCtx := errgroup.WithContext(ctx)
	main := parser.New(parser.Config{Chain: c.chain, Store: st, Decode: decode, Version: version, Decoders: c.decoders,
		Batch: c.batch, Concurrency: 8, PollInterval: c.poll, Log: log})
	balancePass := parser.NewBalances(parser.BalanceConfig{Chain: c.chain, Family: c.family, Store: st, Batch: c.batch, PollInterval: c.poll, Log: log})
	group.Go(func() error { return main.Run(runCtx) })
	group.Go(func() error { return balancePass.Run(runCtx) })
	err = group.Wait()
	log.Info("stopped", "chain", c.chain, "role", "parse")
	return err
}

func hash32(s string) (evm.Hash, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil || len(b) != 32 {
		return evm.Hash{}, fmt.Errorf("%q is not 32 hex bytes", s)
	}
	return evm.Hash(b), nil
}
