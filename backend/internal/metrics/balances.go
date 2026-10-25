package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	BalanceBlocksApplied       = promauto.NewCounterVec(prometheus.CounterOpts{Name: "radar_balance_blocks_applied_total", Help: "Raw blocks decoded by the independent balance pass."}, []string{"chain"})
	TokenBalancesRows          = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "radar_token_balances_rows", Help: "Retained owner/token balance rows."}, []string{"chain"})
	BalanceInvariantViolations = promauto.NewCounter(prometheus.CounterOpts{Name: "radar_balance_invariant_violations_total", Help: "Full-coverage EVM balance/supply conservation mismatches."})
)
