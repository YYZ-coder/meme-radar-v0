package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// InflowRebuildSeconds measures completed projection rebuilds.
var InflowRebuildSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{Name: "radar_inflow_rebuild_seconds", Help: "Duration of a published inflow rebuild.", Buckets: []float64{.05, .1, .25, .5, 1, 2, 5, 7.5, 10, 15, 30, 60}}, []string{"chain", "window"})

// InflowRows is the number of rows in each published chain/window.
var InflowRows = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "radar_inflow_rows", Help: "Rows in the current inflow projection."}, []string{"chain", "window"})

// ProjectionAgeSeconds measures now minus the head cutoff, or +Inf before the first head.
var ProjectionAgeSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "radar_projection_age_seconds", Help: "Age of the published inflow cutoff, +Inf before the first head."}, []string{"chain", "window"})
