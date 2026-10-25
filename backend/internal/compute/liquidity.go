package compute

import (
	"context"
	"time"

	"github.com/meme-radar/radar/internal/store"
)

// Liquidity samples pool reserves and prices pool liquidity (D55, D56).
type Liquidity struct {
	store  *store.Store
	chains []ChainNative
}

func NewLiquidity(s *store.Store, chains []ChainNative) *Liquidity {
	return &Liquidity{store: s, chains: chains}
}

// Step samples changed reserves, then prices every pool's liquidity at now.
func (l *Liquidity) Step(ctx context.Context, now time.Time) error {
	for _, c := range l.chains {
		if err := l.store.SampleLiquidity(ctx, c.Chain); err != nil {
			return err
		}
		if err := l.store.PriceLiquidity(ctx, c.Chain, c.Symbol, now); err != nil {
			return err
		}
	}
	return nil
}
