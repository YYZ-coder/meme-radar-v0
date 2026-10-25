// Package metrics defines the Prometheus metrics every role reports and
// serves them on /metrics (docs/engineering/08-observability.md).
package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	BlocksCommitted = promauto.NewCounterVec(prometheus.CounterOpts{Name: "radar_blocks_committed_total",
		Help: "Blocks written to raw_blocks and chain_blocks, by role (forward, backfill)."}, []string{"chain", "role"})
	ChainHead = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "radar_chain_head",
		Help: "Latest block number the role saw at its source."}, []string{"chain", "role"})
	ForwardPosition = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "radar_forward_position",
		Help: "Forward cursor position."}, []string{"chain"})
	StepErrors = promauto.NewCounterVec(prometheus.CounterOpts{Name: "radar_step_errors_total",
		Help: "Failed steps or passes, by role (forward, backfill, parse, compute, enrich)."}, []string{"role", "chain"})
	RPCRequests = promauto.NewCounterVec(prometheus.CounterOpts{Name: "radar_rpc_requests_total",
		Help: "JSON-RPC requests by endpoint host, method and outcome (ok, rpc_error, rate_limited, error)."}, []string{"host", "method", "outcome"})
	RPCSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{Name: "radar_rpc_duration_seconds",
		Help: "JSON-RPC request duration.", Buckets: []float64{.05, .1, .25, .5, 1, 2, 5, 10, 30}}, []string{"host", "method"})
	BlocksParsed = promauto.NewCounterVec(prometheus.CounterOpts{Name: "radar_blocks_parsed_total",
		Help: "Blocks parse marked parsed."}, []string{"chain"})
	DecodeFailures = promauto.NewCounterVec(prometheus.CounterOpts{Name: "radar_decode_failures_total",
		Help: "Rows written to decode_failures."}, []string{"chain"})
	ComputeStepSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{Name: "radar_compute_step_seconds",
		Help: "Duration of compute steps.", Buckets: []float64{.1, .25, .5, 1, 2, 5, 10, 20, 60}}, []string{"step"})
	ComputeStepErrors = promauto.NewCounterVec(prometheus.CounterOpts{Name: "radar_compute_step_errors_total",
		Help: "Failed compute steps."}, []string{"step"})
	PriceRequests = promauto.NewCounterVec(prometheus.CounterOpts{Name: "radar_price_requests_total",
		Help: "Price source requests by source type and outcome (ok, error)."}, []string{"source", "outcome"})
	EnrichLookups = promauto.NewCounterVec(prometheus.CounterOpts{Name: "radar_enrich_lookups_total",
		Help: "Token supply lookups by outcome (ok, error)."}, []string{"chain", "outcome"})
	// Backlog gauges, set by compute from the database every full pass.
	ForwardLag = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "radar_forward_lag_blocks",
		Help: "Head minus forward position."}, []string{"chain"})
	GapBlocks = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "radar_gap_blocks",
		Help: "Blocks in open gaps (left to backfill)."}, []string{"chain"})
	UnparsedBlocks = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "radar_unparsed_blocks",
		Help: "Stored blocks not parsed yet."}, []string{"chain"})
	UnpricedTrades = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "radar_unpriced_trades_24h",
		Help: "Trades of the last 24 hours without a USD value."}, []string{"chain"})
	Trades24h = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "radar_trades_24h",
		Help: "Trades of the last 24 hours."}, []string{"chain"})
)

// Serve exposes /metrics on addr ("off" or empty disables it) until stop;
// a listen error is logged, not fatal.
func Serve(addr string, log *slog.Logger) (stop func()) {
	if addr == "" || addr == "off" {
		return func() {}
	}
	if log == nil {
		log = slog.Default()
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("metrics listener", "addr", addr, "err", err)
		return func() {}
	}
	go func() {
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server", "err", err)
		}
	}()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}
}
