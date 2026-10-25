package compute

import (
	"context"
	"time"

	"github.com/meme-radar/radar/internal/store"
)

// Stats keeps pool_minute_stats in step with trades.
type Stats struct {
	store  *store.Store
	chains []string
}

func NewStats(s *store.Store, chains []string) *Stats { return &Stats{store: s, chains: chains} }

// Step rebuilds the minutes whose trades changed since the last step.
func (st *Stats) Step(ctx context.Context, now time.Time) error {
	for _, c := range st.chains {
		if err := st.store.RefreshStats(ctx, c, now); err != nil {
			return err
		}
	}
	return nil
}
