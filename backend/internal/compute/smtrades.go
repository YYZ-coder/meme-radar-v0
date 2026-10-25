package compute

import (
	"context"
	"time"

	"github.com/meme-radar/radar/internal/store"
)

// SmTrades maintains the active and building lists' attributed trade copies.
type SmTrades struct {
	store  *store.Store
	chains []string
}

// NewSmTrades returns a compute step for the configured chains.
func NewSmTrades(s *store.Store, chains []string) *SmTrades {
	return &SmTrades{store: s, chains: chains}
}

// Step follows parsed facts and pricing corrections within the 48-hour horizon.
func (st *SmTrades) Step(ctx context.Context, now time.Time) error {
	for _, chain := range st.chains {
		if err := st.store.RefreshSmTrades(ctx, chain, now); err != nil {
			return err
		}
	}
	return nil
}
