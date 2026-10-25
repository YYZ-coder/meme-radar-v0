package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ListBuilds counts completed list build outcomes.
var ListBuilds = promauto.NewCounterVec(prometheus.CounterOpts{Name: "radar_list_builds_total", Help: "List build outcomes (activated, failed)."}, []string{"outcome"})

// ListBuildSeconds records the duration of one claimed list job.
var ListBuildSeconds = promauto.NewHistogram(prometheus.HistogramOpts{Name: "radar_list_build_seconds", Help: "Duration of one claimed list build.", Buckets: []float64{.01, .05, .1, .5, 1, 5, 15, 60}})

// JobsQueued is the compute-observed queued job backlog by kind.
var JobsQueued = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "radar_jobs_queued", Help: "Queued persistent jobs by kind."}, []string{"kind"})

// JobsOldestQueuedSeconds measures the oldest queued job's waiting time.
var JobsOldestQueuedSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "radar_jobs_oldest_queued_seconds", Help: "Age of the oldest queued job, zero when empty."}, []string{"kind"})
