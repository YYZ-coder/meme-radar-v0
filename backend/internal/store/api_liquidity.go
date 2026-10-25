package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// APIPosition is an exact on-chain key used for descending keyset pagination.
type APIPosition struct {
	Time  time.Time `json:"block_time"`
	Block int64     `json:"block_num"`
	Tx    int       `json:"tx_index"`
	Ix    int       `json:"ix_index"`
}

// APILiquidity contains original facts and exact whole-unit quantities.
type APILiquidity struct {
	Position                                        APIPosition
	Kind, Dex                                       string
	TokenAmount, QuoteAmount, QuoteSymbol, ValueUSD *string
	Wallet, TxHash, Pool                            []byte
}

// APILiquidityEvents reads limit rows after a position and reports token existence.
func (s *Store) APILiquidityEvents(ctx context.Context, chain string, token []byte, after *APIPosition, limit int) ([]APILiquidity, bool, error) {
	var at *time.Time
	var block int64
	var tx, ix int
	if after != nil {
		at, block, tx, ix = &after.Time, after.Block, after.Tx, after.Ix
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.block_time,coalesce(e.block_num,0),coalesce(e.tx_index,0),coalesce(e.ix_index,0),
			coalesce(e.kind,''),coalesce(p.dex,''),
			trim_scale(e.token_amount*10::numeric^(-t.supply_decimals))::text,
			trim_scale(e.quote_amount*10::numeric^(-coalesce(qa.decimals,qt.supply_decimals)))::text,
			nullif(e.quote_symbol,''),trim_scale(e.value_usd)::text,e.wallet,e.tx_hash,e.pool_id
		FROM token_info t
		LEFT JOIN LATERAL (
			SELECT l.* FROM liquidity_events l WHERE l.chain=t.chain AND l.token_address=t.address
				AND ($3::timestamptz IS NULL OR (l.block_time,l.block_num,l.tx_index,l.ix_index)<($3,$4::bigint,$5::integer,$6::integer))
			ORDER BY l.block_time DESC,l.block_num DESC,l.tx_index DESC,l.ix_index DESC LIMIT $7
		) e ON true
		LEFT JOIN pools p ON p.chain=t.chain AND p.pool_id=e.pool_id
		LEFT JOIN quote_assets qa ON qa.chain=p.chain AND qa.asset=p.quote_asset
		LEFT JOIN token_info qt ON qt.chain=p.chain AND qt.address=p.quote_asset
		WHERE t.chain=$1 AND t.address=$2
		ORDER BY e.block_time DESC,e.block_num DESC,e.tx_index DESC,e.ix_index DESC`, chain, token, at, block, tx, ix, limit)
	if err != nil {
		return nil, false, fmt.Errorf("store: API liquidity events: %w", err)
	}
	found := false
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (APILiquidity, error) {
		found = true
		var out APILiquidity
		var at *time.Time
		err := r.Scan(&at, &out.Position.Block, &out.Position.Tx, &out.Position.Ix, &out.Kind, &out.Dex, &out.TokenAmount, &out.QuoteAmount, &out.QuoteSymbol, &out.ValueUSD, &out.Wallet, &out.TxHash, &out.Pool)
		if at != nil {
			out.Position.Time = at.UTC()
		}
		return out, err
	})
	if err != nil {
		return nil, false, fmt.Errorf("store: API liquidity events: %w", err)
	}
	out := make([]APILiquidity, 0, len(all))
	for _, r := range all {
		if !r.Position.Time.IsZero() {
			out = append(out, r)
		}
	}
	return out, found, nil
}
