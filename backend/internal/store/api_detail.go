package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// APITokenDetail holds token identity, whole-unit supply and current market data.
type APITokenDetail struct {
	Symbol, Name                              string
	LaunchPlatform                            *string
	LaunchAt                                  *time.Time
	FirstSeenAt                               time.Time
	Decimals                                  *int16
	Supply                                    *string
	Creator, MainPool                         []byte
	Dex, QuoteSymbol                          *string
	Price, MarketCap, Liquidity, ATHMarketCap *string
}

// APIToken reads parse/enrich metadata and compute-owned market projections.
func (s *Store) APIToken(ctx context.Context, chain string, token []byte, native bool) (APITokenDetail, bool, error) {
	var out APITokenDetail
	err := s.pool.QueryRow(ctx, `
		SELECT t.symbol,t.name,t.launch_platform,t.launch_at,t.first_seen_at,t.supply_decimals,
			trim_scale(t.supply*10::numeric^(-t.supply_decimals))::text,t.creator,m.main_pool_id,p.dex,nullif(p.quote_symbol,''),
			trim_scale(CASE WHEN $3::boolean THEN m.price_native ELSE m.price_usd END)::text,
			trim_scale(CASE WHEN $3::boolean THEN m.market_cap_native ELSE m.market_cap_usd END)::text,
			trim_scale(CASE WHEN $3::boolean THEN m.liquidity_native ELSE m.liquidity_usd END)::text,
			trim_scale(CASE WHEN NOT $3::boolean THEN m.ath_market_cap_usd END)::text
		FROM token_info t LEFT JOIN token_market m ON m.chain=t.chain AND m.address=t.address
		LEFT JOIN pools p ON p.chain=t.chain AND p.pool_id=m.main_pool_id
		WHERE t.chain=$1 AND t.address=$2`, chain, token, native).Scan(&out.Symbol, &out.Name, &out.LaunchPlatform, &out.LaunchAt, &out.FirstSeenAt, &out.Decimals, &out.Supply, &out.Creator, &out.MainPool, &out.Dex, &out.QuoteSymbol, &out.Price, &out.MarketCap, &out.Liquidity, &out.ATHMarketCap)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, fmt.Errorf("store: API token: %w", err)
	}
	return out, true, nil
}

// APISameName returns at most ten other equal-symbol tokens, newest trade first.
func (s *Store) APISameName(ctx context.Context, chain string, token []byte, symbol string, now time.Time) ([]APISearchToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.chain::text,t.address,t.symbol,t.name,t.launch_platform,t.launch_at,
			trim_scale(m.market_cap_usd)::text,floor(extract(epoch FROM ($4::timestamptz-t.launch_at)))::bigint,m.last_trade_at
		FROM token_info t LEFT JOIN token_market m ON m.chain=t.chain AND m.address=t.address
		WHERE lower(t.symbol)=lower($3) AND NOT (t.chain=$1 AND t.address=$2)
		ORDER BY m.last_trade_at DESC NULLS LAST,t.chain,t.address LIMIT 10`, chain, token, symbol, now)
	if err != nil {
		return nil, fmt.Errorf("store: API same name: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (APISearchToken, error) {
		var t APISearchToken
		err := r.Scan(&t.Chain, &t.Address, &t.Symbol, &t.Name, &t.LaunchPlatform, &t.LaunchAt, &t.MarketCapUSD, &t.AgeSeconds, &t.LastTradeAt)
		return t, err
	})
	if err != nil {
		return nil, fmt.Errorf("store: API same name: %w", err)
	}
	return out, nil
}
