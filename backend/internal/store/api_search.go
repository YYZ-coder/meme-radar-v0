package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// APISearchToken is token metadata with optional current market values.
type APISearchToken struct {
	Chain          string
	Address        []byte
	Symbol, Name   string
	LaunchPlatform *string
	LaunchAt       *time.Time
	MarketCapUSD   *string
	AgeSeconds     *int64
	LastTradeAt    *time.Time
}

// APISearchTokens searches a literal name/symbol prefix or a parsed address.
// family is empty, solana or evm; an empty chain includes all supported chains.
func (s *Store) APISearchTokens(ctx context.Context, q, chain, family string, address []byte, limit int, now time.Time) ([]APISearchToken, error) {
	pattern := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
	rows, err := s.pool.Query(ctx, `
		SELECT t.chain::text,t.address,t.symbol,t.name,t.launch_platform,t.launch_at,
			trim_scale(m.market_cap_usd)::text,
			floor(extract(epoch FROM ($6::timestamptz-t.launch_at)))::bigint,m.last_trade_at
		FROM token_info t
		LEFT JOIN token_market m ON m.chain=t.chain AND m.address=t.address
		WHERE ($2='' OR t.chain=nullif($2,'')::chain_id) AND (
			($3='solana' AND t.chain='solana' AND t.address=$4::bytea)
			OR ($3='evm' AND t.chain<>'solana' AND t.address=$4::bytea)
			OR ($3='' AND (lower(t.symbol) LIKE lower($1) OR lower(t.name) LIKE lower($1)))
		)
		ORDER BY m.market_cap_usd DESC NULLS LAST,m.last_trade_at DESC NULLS LAST,t.chain,t.address
		LIMIT $5`, pattern, chain, family, address, limit, now)
	if err != nil {
		return nil, fmt.Errorf("store: API search: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (APISearchToken, error) {
		var t APISearchToken
		err := r.Scan(&t.Chain, &t.Address, &t.Symbol, &t.Name, &t.LaunchPlatform, &t.LaunchAt, &t.MarketCapUSD, &t.AgeSeconds, &t.LastTradeAt)
		return t, err
	})
	if err != nil {
		return nil, fmt.Errorf("store: API search: %w", err)
	}
	return out, nil
}
