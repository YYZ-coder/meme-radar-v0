// Command radar runs Meme Radar backend roles.
//
//	radar api --store=mock|pg [--addr=127.0.0.1:8080] [--debug-addr=127.0.0.1:6060]
//	radar ingest --chain=solana|base|bsc [--rpc-url=URL] [--rps=N] [--block-rps=N] [--prefetch=N]
//
//	radar parse --chain=solana [--batch=N]
//	radar compute [--interval=10s]
//	radar backfill --chain=bsc
//	radar enrich [--interval=30s]
//	radar prune-raw --before=YYYY-MM-DD [--chain=bsc]
//
// ingest follows one chain block by block into PostgreSQL (RADAR_DATABASE_URL),
// using the chain's free public RPC endpoint unless --rpc-url is given; parse
// decodes the stored blocks into tokens, pools, trades and liquidity events.
//
// The admin token comes from RADAR_ADMIN_TOKEN; the mock store defaults it to
// "mock-admin" (the frontend mock's token) so local smoke tests work unchanged.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/metrics"
)

type apiConfig struct {
	addr, store, adminToken, debugAddr string
	dbURL                              string
	cfg                                config.Config
}

func parseConfig(args []string, getenv func(string) string) (apiConfig, error) {
	if len(args) == 0 || args[0] != "api" {
		return apiConfig{}, errors.New("usage: radar api --store=mock|pg [--addr=host:port] [--debug-addr=127.0.0.1:port]")
	}
	fs := flag.NewFlagSet("api", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var c apiConfig
	fs.StringVar(&c.addr, "addr", "127.0.0.1:8080", "listen address")
	fs.StringVar(&c.store, "store", "", "data store (mock or pg)")
	fs.StringVar(&c.debugAddr, "debug-addr", "", "loopback address for pprof; empty disables it")
	if err := fs.Parse(args[1:]); err != nil {
		return apiConfig{}, err
	}
	switch c.store {
	case "mock":
	case "pg":
		if c.dbURL = getenv("RADAR_DATABASE_URL"); c.dbURL == "" {
			return apiConfig{}, errors.New("RADAR_DATABASE_URL is required for --store=pg")
		}
		cfg, err := loadConfig("", getenv)
		if err != nil {
			return apiConfig{}, err
		}
		c.cfg = cfg
	default:
		return apiConfig{}, fmt.Errorf("unsupported --store=%q; use --store=mock or --store=pg", c.store)
	}
	if err := checkLoopback(c.debugAddr); err != nil {
		return apiConfig{}, err
	}
	c.adminToken = getenv("RADAR_ADMIN_TOKEN")
	if c.adminToken == "" && c.store == "mock" {
		c.adminToken = "mock-admin"
	}
	return c, nil
}

// checkLoopback accepts an empty address or a loopback host:port.
func checkLoopback(addr string) error {
	if addr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return fmt.Errorf("--debug-addr must be a loopback address, got %q", addr)
	}
	return nil
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] != "prune-raw" {
		defer metrics.Serve(metricsAddr(os.Getenv), log)()
	}
	if len(os.Args) > 1 && os.Args[1] == "parse" {
		c, err := parseParse(os.Args[1:], os.Getenv)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := runParse(ctx, c, log); err != nil {
			log.Error("radar exited", "err", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "backfill" {
		c, err := parseBackfill(os.Args[1:], os.Getenv)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := runBackfill(ctx, c, log); err != nil {
			log.Error("radar exited", "err", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "prune-raw" {
		c, err := parsePruneRaw(os.Args[1:], os.Getenv)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := runPruneRaw(ctx, c, log); err != nil {
			log.Error("radar exited", "err", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "enrich" {
		c, err := parseEnrich(os.Args[1:], os.Getenv)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := runEnrich(ctx, c, log); err != nil {
			log.Error("radar exited", "err", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "compute" {
		c, err := parseCompute(os.Args[1:], os.Getenv)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := runCompute(ctx, c, log); err != nil {
			log.Error("radar exited", "err", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "ingest" {
		c, err := parseIngest(os.Args[1:], os.Getenv)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := runIngest(ctx, c, log); err != nil {
			log.Error("radar exited", "err", err)
			os.Exit(1)
		}
		return
	}
	c, err := parseConfig(os.Args[1:], os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := run(ctx, c, log); err != nil {
		log.Error("radar exited", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, c apiConfig, log *slog.Logger) error {
	b, closeBackend, err := openAPIBackend(ctx, c)
	if err != nil {
		return err
	}
	defer closeBackend()
	if c.store == "mock" && os.Getenv("RADAR_ADMIN_TOKEN") == "" {
		log.Warn("RADAR_ADMIN_TOKEN unset; mock store uses the default admin token")
	}
	api := &http.Server{
		Addr: c.addr,
		Handler: httpapi.New(b, httpapi.Options{
			AdminToken: c.adminToken, AdminDisabled: c.store == "pg" && c.adminToken == "",
			Logger: log, DemoData: c.store == "mock",
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	servers := []*http.Server{api}
	if c.debugAddr != "" {
		servers = append(servers, debugServer(c.debugAddr))
	}
	errc := make(chan error, len(servers))
	for _, s := range servers {
		go func() {
			defer func() { // D112: no unguarded goroutines
				if v := recover(); v != nil {
					errc <- fmt.Errorf("listener %s panicked: %v", s.Addr, v)
				}
			}()
			log.Info("listening", "addr", s.Addr, "store", c.store)
			if err := s.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				errc <- err
				return
			}
			errc <- nil
		}()
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, s := range servers {
		if err := s.Shutdown(shutdown); err != nil {
			return err
		}
	}
	log.Info("stopped")
	return nil
}

// debugServer serves pprof on its own mux, never on the API listener.
func debugServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}

// serveDebug starts a pprof server for a role without its own listener set.
func serveDebug(addr string, log *slog.Logger) (stop func()) {
	s := debugServer(addr)
	go func() {
		if err := s.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Error("debug listener", "addr", addr, "err", err)
		}
	}()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	}
}
