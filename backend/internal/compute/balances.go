package compute

import (
	"context"
	"fmt"
	"time"

	"log/slog"

	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/parse/balances"
	"github.com/meme-radar/radar/internal/store"
)

// Holdings maintains owner balances from replaceable block deltas.
type Holdings struct {
	store  *store.Store
	chains []string
	log    *slog.Logger
}

// NewBalances returns the compute-owned balance accumulator.
func NewBalances(s *store.Store, chains []string) *Holdings {
	return &Holdings{store: s, chains: chains}
}

// Step applies journal corrections for reparsed and backfilled blocks.
func (b *Holdings) Step(ctx context.Context, now time.Time) error {
	for _, chain := range b.chains {
		if err := b.store.RefreshBalances(ctx, chain, now, balances.Version); err != nil {
			return err
		}
		violations, err := b.store.BalanceViolations(ctx, chain)
		if err != nil {
			return err
		}
		logger := b.log
		if logger == nil {
			logger = slog.Default()
		}
		for _, v := range violations {
			metrics.BalanceInvariantViolations.Inc()
			logger.Warn("balance conservation mismatch", "chain", chain, "role", "compute", "token", fmt.Sprintf("%x", v.Token), "actual", v.Actual, "expected", v.Expected)
		}
	}
	return nil
}
