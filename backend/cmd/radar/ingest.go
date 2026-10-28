package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/ingest"
	"github.com/meme-radar/radar/internal/ingest/evmrpc"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/ingest/solanarpc"
	"github.com/meme-radar/radar/internal/store"
)

const ingestUsage = "usage: radar ingest --chain=NAME [--config=FILE] [--rpc-url=URL] [--rps=N] [--block-rps=N] [--prefetch=N] [--debug-addr=127.0.0.1:port] (RADAR_DATABASE_URL required)"

// ingestConfig is the parsed `radar ingest` command line.
type ingestConfig struct {
	chain, family string
	rpcURL, dbURL string
	rpcFallback   string
	// rps limits every call; blockRPS additionally limits block requests.
	rps, blockRPS  float64
	followHead     bool
	confirmations  uint64
	reorgDepth     uint64
	backfillBlocks uint64
	maxLag         uint64
	// skipFill leaves gap filling to the backfill role.
	skipFill                      bool
	rawRetention, ledgerRetention time.Duration
	prefetch                      int
	poll                          time.Duration
	debugAddr                     string
}

// loadConfig reads the chain configuration from --config, RADAR_CONFIG or
// the embedded defaults.
func loadConfig(flagPath string, getenv func(string) string) (config.Config, error) {
	path := flagPath
	if path == "" {
		path = getenv("RADAR_CONFIG")
	}
	return config.Load(path)
}

func parseIngest(args []string, getenv func(string) string) (ingestConfig, error) {
	if len(args) == 0 || args[0] != "ingest" {
		return ingestConfig{}, errors.New(ingestUsage)
	}
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	chain := fs.String("chain", "", "solana, base or bsc")
	url := fs.String("rpc-url", "", "JSON-RPC endpoint (default: the chain's free public endpoint)")
	rps := fs.Float64("rps", 0, "requests per second for all calls (default: preset)")
	blockRPS := fs.Float64("block-rps", 0, "requests per second for getBlock (Solana; default: preset)")
	prefetch := fs.Int("prefetch", 0, "blocks fetched concurrently, 1–64 (default: preset)")
	debugAddr := fs.String("debug-addr", "", "loopback address for pprof; empty disables it")
	configPath := fs.String("config", "", "chain configuration file (default: RADAR_CONFIG, else built-in)")
	if err := fs.Parse(args[1:]); err != nil {
		return ingestConfig{}, fmt.Errorf("%w\n%s", err, ingestUsage)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	cfg, err := loadConfig(*configPath, getenv)
	if err != nil {
		return ingestConfig{}, err
	}
	ch, ok := cfg.Chain(*chain)
	if !ok {
		return ingestConfig{}, fmt.Errorf("--chain=%q is not in the configuration\n%s", *chain, ingestUsage)
	}
	p := ch.Ingest
	c := ingestConfig{chain: ch.Name, family: ch.Family, rpcURL: p.RPCURL, rps: p.RPS, blockRPS: p.BlockRPS,
		followHead: p.FollowHead, confirmations: p.Confirmations, reorgDepth: p.ReorgDepth, prefetch: p.Prefetch,
		poll: time.Duration(p.Poll), debugAddr: *debugAddr}
	if endpoints := ch.RPCURLs(p.RPCURL); len(endpoints) > 1 {
		c.rpcURL, c.rpcFallback = endpoints[0], endpoints[1]
	}
	if *url != "" {
		c.rpcURL = *url
		c.rpcFallback = ""
	}
	if !strings.HasPrefix(c.rpcURL, "https://") && !strings.HasPrefix(c.rpcURL, "http://") {
		return ingestConfig{}, fmt.Errorf("--rpc-url must be http(s), got %q", c.rpcURL)
	}
	if set["rps"] {
		if *rps <= 0 {
			return ingestConfig{}, errors.New("--rps must be positive")
		}
		c.rps = *rps
	}
	if set["block-rps"] {
		if *blockRPS < 0 {
			return ingestConfig{}, errors.New("--block-rps must not be negative")
		}
		c.blockRPS = *blockRPS
	}
	if set["prefetch"] {
		if *prefetch < 1 || *prefetch > 64 {
			return ingestConfig{}, errors.New("--prefetch must be between 1 and 64")
		}
		c.prefetch = *prefetch
	}
	c.backfillBlocks = uint64(time.Duration(ch.Backfill.Hours*float64(time.Hour)) / time.Duration(p.BlockInterval))
	c.skipFill = ch.Backfill.Hours > 0 // the backfill role fills gaps
	c.maxLag = p.MaxLagBlocks
	c.rawRetention = time.Duration(cfg.Retention.RawBlocksDays) * 24 * time.Hour
	c.ledgerRetention = time.Duration(cfg.Retention.LedgerDays) * 24 * time.Hour
	if err := checkLoopback(c.debugAddr); err != nil {
		return ingestConfig{}, err
	}
	if c.dbURL = getenv("RADAR_DATABASE_URL"); c.dbURL == "" {
		return ingestConfig{}, errors.New("RADAR_DATABASE_URL is required")
	}
	return c, nil
}

func runIngest(ctx context.Context, c ingestConfig, log *slog.Logger) error {
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
	unlock, err := st.LockChain(ctx, c.chain)
	if err != nil {
		return err
	}
	defer unlock()
	if c.debugAddr != "" {
		stop := serveDebug(c.debugAddr, log)
		defer stop()
	}
	var perMethod map[string]*rate.Limiter
	if c.blockRPS > 0 {
		perMethod = map[string]*rate.Limiter{"getBlock": rate.NewLimiter(rate.Limit(c.blockRPS), 1)}
	}
	newSource := func(url string) ingest.Source {
		rpc := jsonrpc.New(url, &http.Client{Timeout: 30 * time.Second}, rate.NewLimiter(rate.Limit(c.rps), 1), perMethod, c.rpcFallback)
		if c.family == "solana" {
			return solanarpc.New(rpc)
		}
		return evmrpc.New(rpc)
	}
	src := newSource(c.rpcURL)
	log.Info("ingest started", "chain", c.chain, "rpc_host", rpcHost(c.rpcURL), "follow_head", c.followHead, "window_blocks", c.backfillBlocks,
		"skip_fill", c.skipFill, "prefetch", c.prefetch)
	err = ingest.NewFollower(ingest.Config{
		Chain: c.chain, Source: src, SourceName: c.chain + "-rpc", Store: st, FollowHead: c.followHead,
		Confirmations: c.confirmations, ReorgDepth: c.reorgDepth, Backfill: c.backfillBlocks, MaxLag: c.maxLag, SkipFill: c.skipFill, Prefetch: c.prefetch,
		RawRetention: c.rawRetention, LedgerRetention: c.ledgerRetention, PollInterval: c.poll, Log: log,
	}).Run(ctx)
	log.Info("stopped", "chain", c.chain)
	return err
}

const backfillUsage = "usage: radar backfill --chain=NAME [--config=FILE] (RADAR_DATABASE_URL required; settings from the chain's backfill section)"

// backfillConfig is the backfill role: it fills gaps (stream_gaps) within
// window blocks behind the head, newest first, without following the head.
type backfillConfig struct {
	chain, family, rpcURL, dbURL string
	rpcFallback                  string
	rps, blockRPS                float64
	confirmations, window        uint64
	pauseLag                     uint64
	prefetch                     int
	poll                         time.Duration
}

func parseBackfill(args []string, getenv func(string) string) (backfillConfig, error) {
	if len(args) == 0 || args[0] != "backfill" {
		return backfillConfig{}, errors.New(backfillUsage)
	}
	fs := flag.NewFlagSet("backfill", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	chain := fs.String("chain", "", "chain to backfill")
	configPath := fs.String("config", "", "chain configuration file (default: RADAR_CONFIG, else built-in)")
	if err := fs.Parse(args[1:]); err != nil {
		return backfillConfig{}, fmt.Errorf("%w\n%s", err, backfillUsage)
	}
	cfg, err := loadConfig(*configPath, getenv)
	if err != nil {
		return backfillConfig{}, err
	}
	ch, ok := cfg.Chain(*chain)
	if !ok {
		return backfillConfig{}, fmt.Errorf("--chain=%q is not in the configuration\n%s", *chain, backfillUsage)
	}
	b := ch.Backfill
	if b.Hours <= 0 {
		return backfillConfig{}, fmt.Errorf("chain %s has no backfill window (backfill.hours)", ch.Name)
	}
	c := backfillConfig{chain: ch.Name, family: ch.Family, rpcURL: b.RPCURL, rps: b.RPS, blockRPS: b.BlockRPS,
		confirmations: ch.Ingest.Confirmations, pauseLag: b.PauseLag, prefetch: b.Prefetch, poll: time.Duration(ch.Ingest.Poll),
		window: uint64(time.Duration(b.Hours*float64(time.Hour)) / time.Duration(ch.Ingest.BlockInterval))}
	endpoints := ch.RPCURLs(c.rpcURL)
	c.rpcURL = endpoints[0]
	if len(endpoints) > 1 {
		c.rpcFallback = endpoints[1]
	}
	if c.dbURL = getenv("RADAR_DATABASE_URL"); c.dbURL == "" {
		return backfillConfig{}, errors.New("RADAR_DATABASE_URL is required")
	}
	return c, nil
}

func runBackfill(ctx context.Context, c backfillConfig, log *slog.Logger) error {
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
	unlock, err := st.LockChain(ctx, "backfill:"+c.chain) // separate from the forward follower's lock
	if err != nil {
		return err
	}
	defer unlock()
	var perMethod map[string]*rate.Limiter
	if c.blockRPS > 0 {
		perMethod = map[string]*rate.Limiter{"getBlock": rate.NewLimiter(rate.Limit(c.blockRPS), 1)}
	}
	rpc := jsonrpc.New(c.rpcURL, &http.Client{Timeout: 30 * time.Second}, rate.NewLimiter(rate.Limit(c.rps), 1), perMethod, c.rpcFallback)
	var src ingest.Source = evmrpc.New(rpc)
	if c.family == "solana" {
		src = solanarpc.New(rpc)
	}
	log.Info("backfill started", "chain", c.chain, "rpc_host", rpcHost(c.rpcURL), "window_blocks", c.window, "prefetch", c.prefetch)
	err = ingest.NewFollower(ingest.Config{Chain: c.chain, Source: src, SourceName: c.chain + "-backfill", Store: st,
		Confirmations: c.confirmations, Backfill: c.window, GapsOnly: true, PauseLag: c.pauseLag, Prefetch: c.prefetch, PollInterval: c.poll, Log: log}).Run(ctx)
	log.Info("stopped", "chain", c.chain, "role", "backfill")
	return err
}

const pruneRawUsage = "usage: radar prune-raw --before=YYYY-MM-DD [--chain=NAME] [--config=FILE] (RADAR_DATABASE_URL required)"

type pruneRawConfig struct {
	chain, dbURL string
	before       time.Time
}

func parsePruneRaw(args []string, getenv func(string) string) (pruneRawConfig, error) {
	if len(args) == 0 || args[0] != "prune-raw" {
		return pruneRawConfig{}, errors.New(pruneRawUsage)
	}
	fs := flag.NewFlagSet("prune-raw", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	before := fs.String("before", "", "delete raw blocks older than this UTC date")
	chain := fs.String("chain", "", "only this chain (default: whole days of every chain)")
	configPath := fs.String("config", "", "chain configuration file (default: RADAR_CONFIG, else built-in)")
	if err := fs.Parse(args[1:]); err != nil {
		return pruneRawConfig{}, fmt.Errorf("%w\n%s", err, pruneRawUsage)
	}
	t, err := time.Parse("2006-01-02", *before)
	if err != nil {
		return pruneRawConfig{}, fmt.Errorf("--before must be YYYY-MM-DD\n%s", pruneRawUsage)
	}
	if *chain != "" {
		cfg, err := loadConfig(*configPath, getenv)
		if err != nil {
			return pruneRawConfig{}, err
		}
		if _, ok := cfg.Chain(*chain); !ok {
			return pruneRawConfig{}, fmt.Errorf("--chain=%q is not in the configuration", *chain)
		}
	}
	c := pruneRawConfig{chain: *chain, before: t}
	if c.dbURL = getenv("RADAR_DATABASE_URL"); c.dbURL == "" {
		return pruneRawConfig{}, errors.New("RADAR_DATABASE_URL is required")
	}
	return c, nil
}

func runPruneRaw(ctx context.Context, c pruneRawConfig, log *slog.Logger) error {
	st, err := store.Open(ctx, c.dbURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.PruneRaw(ctx, c.before, c.chain); err != nil {
		return err
	}
	log.Info("raw blocks pruned", "before", c.before.Format("2006-01-02"), "chain", c.chain)
	return nil
}

// rpcHost excludes authentication paths, queries and user information from logs.
func rpcHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Host
}
