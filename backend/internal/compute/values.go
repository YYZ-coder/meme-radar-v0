package compute

import (
	"context"
	"time"

	"github.com/meme-radar/radar/internal/store"
)

// Values fills USD values of trades and liquidity events (D138).
type Values struct {
	store  *store.Store
	chains []ChainNative
}

func NewValues(s *store.Store, chains []ChainNative) *Values {
	return &Values{store: s, chains: chains}
}

// Step prices every unpriced fact since since that can be priced now.
func (v *Values) Step(ctx context.Context, since time.Time) error {
	for _, c := range v.chains {
		if err := v.store.FillValues(ctx, c.Chain, c.Symbol, since); err != nil {
			return err
		}
	}
	return nil
}
