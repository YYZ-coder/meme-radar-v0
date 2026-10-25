package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// APIRequests counts completed responses by stable mux pattern and status.
	APIRequests = promauto.NewCounterVec(prometheus.CounterOpts{Name: "radar_api_requests_total", Help: "API responses by route pattern and HTTP status."}, []string{"route", "status"})
	// APISeconds records complete request durations by stable mux pattern.
	APISeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{Name: "radar_api_duration_seconds", Help: "API request duration by route pattern.", Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2, 5}}, []string{"route"})
)
