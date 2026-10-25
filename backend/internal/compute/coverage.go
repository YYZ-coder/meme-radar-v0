package compute

import (
	"context"

	"github.com/meme-radar/radar/internal/store"
)

// Coverage keeps coverage_ranges (D140).
type Coverage struct {
	store  *store.Store
	chains []string
}

func NewCoverage(s *store.Store, chains []string) *Coverage {
	return &Coverage{store: s, chains: chains}
}

// Step rebuilds every chain's coverage ranges.
func (c *Coverage) Step(ctx context.Context) error {
	for _, ch := range c.chains {
		if err := c.store.RefreshCoverage(ctx, ch); err != nil {
			return err
		}
	}
	return nil
}
