package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// MetadataFetches counts metadata outcomes, separating SSRF blocks from failures.
var MetadataFetches = promauto.NewCounterVec(prometheus.CounterOpts{Name: "radar_metadata_fetch_total", Help: "Metadata outcomes (ok, failed, skipped, blocked) and auxiliary events (skipped_host, raw_omitted)."}, []string{"chain", "outcome"})

// MetadataFetchSeconds measures one token's metadata attempt, including fallbacks.
var MetadataFetchSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{Name: "radar_metadata_fetch_seconds", Help: "Time to resolve and fetch a token metadata document.", Buckets: []float64{.05, .1, .25, .5, 1, 2, 5, 10, 30}}, []string{"chain"})
