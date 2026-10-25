package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// APIPool contains pool metadata, whole-unit reserves and optional USD liquidity.
type APIPool struct {
	ID                                                    []byte
	Dex, Protocol, Status                                 string
	QuoteSymbol, LiquidityUSD, ReserveToken, ReserveQuote *string
	CreatedAt                                             *time.Time
	IsMain                                                bool
}

// APITokenPools returns a token's pools and whether the token exists, in one read.
func (s *Store) APITokenPools(ctx context.Context, chain string, token []byte) ([]APIPool, bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.pool_id,coalesce(p.dex,''),coalesce(p.protocol,''),coalesce(p.status,''),
			nullif(p.quote_symbol,''),trim_scale(pm.liquidity_usd)::text,
			trim_scale(p.reserve_token*10::numeric^(-t.supply_decimals))::text,
			trim_scale(p.reserve_quote*10::numeric^(-coalesce(qa.decimals,qt.supply_decimals)))::text,
			p.created_at,coalesce(p.pool_id=m.main_pool_id,false)
		FROM token_info t
		LEFT JOIN pools p ON p.chain=t.chain AND p.token_address=t.address
		LEFT JOIN pool_market pm ON pm.chain=p.chain AND pm.pool_id=p.pool_id
		LEFT JOIN token_market m ON m.chain=t.chain AND m.address=t.address
		LEFT JOIN quote_assets qa ON qa.chain=p.chain AND qa.asset=p.quote_asset
		LEFT JOIN token_info qt ON qt.chain=p.chain AND qt.address=p.quote_asset
		WHERE t.chain=$1 AND t.address=$2
		ORDER BY coalesce(p.pool_id=m.main_pool_id,false) DESC,p.pool_id`, chain, token)
	if err != nil {
		return nil, false, fmt.Errorf("store: API pools: %w", err)
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (APIPool, error) {
		var p APIPool
		err := r.Scan(&p.ID, &p.Dex, &p.Protocol, &p.Status, &p.QuoteSymbol, &p.LiquidityUSD, &p.ReserveToken, &p.ReserveQuote, &p.CreatedAt, &p.IsMain)
		return p, err
	})
	if err != nil {
		return nil, false, fmt.Errorf("store: API pools: %w", err)
	}
	out := make([]APIPool, 0, len(all))
	for _, p := range all {
		if p.ID != nil {
			out = append(out, p)
		}
	}
	return out, len(all) > 0, nil
}
