package compute

import (
	"context"
	"github.com/meme-radar/radar/internal/store"
)

// ATH keeps each chain's recorded price high and corresponding market cap.
type ATH struct {
	store  *store.Store
	chains []string
}

// NewATH constructs a high-water mark step for the configured chains.
func NewATH(s *store.Store, chains []string) *ATH { return &ATH{store: s, chains: chains} }

// Step records newly observed highs without lowering an existing high.
func (a *ATH) Step(ctx context.Context) error {
	for _, chain := range a.chains {
		if err := a.store.RefreshATH(ctx, chain); err != nil {
			return err
		}
	}
	return nil
}
