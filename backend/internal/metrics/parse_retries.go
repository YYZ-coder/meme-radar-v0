package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ParseRetryBlocks counts distinct blocks reset by explicit retry requests.
var ParseRetryBlocks = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "radar_parse_retry_blocks_total", Help: "Distinct blocks reset by the parse role for explicit retry requests. Does not imply successful decoding.",
}, []string{"chain"})
