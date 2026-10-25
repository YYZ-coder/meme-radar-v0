package compute

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/meme-radar/radar/internal/store"
)

// Balances reads token.balanceOf(holder) at the latest block.
type Balances interface {
	BalanceOf(ctx context.Context, token, holder []byte) (*big.Int, error)
}

// V3Liquidity samples concentrated-liquidity pools that hold their own
// balances (PancakeSwap V3): at most once a minute per recently traded pool.
// Singleton-vault pools (Uniswap v4, Infinity) cannot be read per pool and
// keep NULL liquidity.
type V3Liquidity struct {
	store     *store.Store
	chain     string
	protocols []string
	balances  Balances
}

func NewV3Liquidity(s *store.Store, chain string, protocols []string, b Balances) *V3Liquidity {
	return &V3Liquidity{store: s, chain: chain, protocols: protocols, balances: b}
}

// Step samples each due pool's balances at now.
func (v *V3Liquidity) Step(ctx context.Context, now time.Time) error {
	pools, err := v.store.PoolsToSample(ctx, v.chain, v.protocols, now)
	if err != nil {
		return err
	}
	for _, p := range pools {
		tb, err := v.balances.BalanceOf(ctx, p.Token, p.Pool)
		if err != nil {
			return fmt.Errorf("compute: pool %x token balance: %w", p.Pool, err)
		}
		qb, err := v.balances.BalanceOf(ctx, p.Quote, p.Pool)
		if err != nil {
			return fmt.Errorf("compute: pool %x quote balance: %w", p.Pool, err)
		}
		if err := v.store.SaveBalanceSample(ctx, v.chain, p.Pool, tb.String(), qb.String(), now); err != nil {
			return err
		}
	}
	return nil
}
