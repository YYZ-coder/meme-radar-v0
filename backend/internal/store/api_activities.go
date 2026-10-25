package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// APIActivity combines a trade or liquidity change with nullable trade-only data.
type APIActivity struct {
	APILiquidity
	Attribution, PriceUSD, MarketCapUSD *string
}

// APIActivities reads a filtered, position-ordered union and token existence.
func (s *Store) APIActivities(ctx context.Context, chain string, token []byte, after *APIPosition, kind string, from, to *time.Time, labelsAvailable bool, limit int) ([]APIActivity, bool, error) {
	var at *time.Time
	var block int64
	var tx, ix int
	if after != nil {
		at, block, tx, ix = &after.Time, after.Block, after.Tx, after.Ix
	}
	rows, err := s.pool.Query(ctx, `
		WITH facts AS (
			SELECT chain,token_address,block_time,block_num,tx_index,ix_index,side AS kind,token_amount,quote_amount,
				quote_symbol,value_usd,wallet,tx_hash,pool_id,attribution,price_usd,true AS trade FROM trades
			UNION ALL
			SELECT chain,token_address,block_time,block_num,tx_index,ix_index,kind,token_amount,quote_amount,
				quote_symbol,value_usd,wallet,tx_hash,pool_id,NULL::text,NULL::numeric,false FROM liquidity_events
		)
		SELECT e.block_time,coalesce(e.block_num,0),coalesce(e.tx_index,0),coalesce(e.ix_index,0),
			coalesce(e.kind,''),coalesce(p.dex,''),trim_scale(e.token_amount*10::numeric^(-t.supply_decimals))::text,
			trim_scale(e.quote_amount*10::numeric^(-coalesce(qa.decimals,qt.supply_decimals)))::text,
			nullif(e.quote_symbol,''),trim_scale(e.value_usd)::text,e.wallet,e.tx_hash,e.pool_id,e.attribution,
			trim_scale(e.price_usd)::text,trim_scale(e.price_usd*m.circulating_supply)::text
		FROM token_info t
		LEFT JOIN token_market m ON m.chain=t.chain AND m.address=t.address
		LEFT JOIN LATERAL (
			SELECT f.* FROM facts f WHERE f.chain=t.chain AND f.token_address=t.address
				AND ($3::timestamptz IS NULL OR (f.block_time,f.block_num,f.tx_index,f.ix_index)<($3,$4::bigint,$5::integer,$6::integer))
				AND ($7::text IN ('','all') OR ($7='trade' AND f.trade) OR ($7='liquidity' AND NOT f.trade))
				AND ($8::timestamptz IS NULL OR f.block_time >= $8) AND ($9::timestamptz IS NULL OR f.block_time < $9)
				AND $10::boolean
			ORDER BY f.block_time DESC,f.block_num DESC,f.tx_index DESC,f.ix_index DESC LIMIT $11
		) e ON true
		LEFT JOIN pools p ON p.chain=t.chain AND p.pool_id=e.pool_id
		LEFT JOIN quote_assets qa ON qa.chain=p.chain AND qa.asset=p.quote_asset
		LEFT JOIN token_info qt ON qt.chain=p.chain AND qt.address=p.quote_asset
		WHERE t.chain=$1 AND t.address=$2
		ORDER BY e.block_time DESC,e.block_num DESC,e.tx_index DESC,e.ix_index DESC`, chain, token, at, block, tx, ix, kind, from, to, labelsAvailable, limit)
	if err != nil {
		return nil, false, fmt.Errorf("store: API activities: %w", err)
	}
	found := false
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (APIActivity, error) {
		found = true
		var a APIActivity
		var at *time.Time
		err := r.Scan(&at, &a.Position.Block, &a.Position.Tx, &a.Position.Ix, &a.Kind, &a.Dex, &a.TokenAmount, &a.QuoteAmount, &a.QuoteSymbol, &a.ValueUSD, &a.Wallet, &a.TxHash, &a.Pool, &a.Attribution, &a.PriceUSD, &a.MarketCapUSD)
		if at != nil {
			a.Position.Time = at.UTC()
		}
		return a, err
	})
	if err != nil {
		return nil, false, fmt.Errorf("store: API activities: %w", err)
	}
	out := make([]APIActivity, 0, len(all))
	for _, a := range all {
		if !a.Position.Time.IsZero() {
			out = append(out, a)
		}
	}
	return out, found, nil
}
