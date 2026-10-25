package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// APIQuotePrice is the latest native price and its same-minute 24h comparison.
// Nil price/time fields mean that the requested symbol has no recorded price.
type APIQuotePrice struct {
	Symbol                 string
	PriceUSD, Change24hPct *string
	UpdatedAt              *time.Time
}

// APIQuotePrices reads one row per symbol, preserving the requested order.
func (s *Store) APIQuotePrices(ctx context.Context, symbols []string) ([]APIQuotePrice, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT wanted.symbol,trim_scale(latest.price_usd)::text,
			trim_scale((latest.price_usd-previous.price_usd)/nullif(previous.price_usd,0)*100)::text,
			latest.updated_at
		FROM unnest($1::text[]) WITH ORDINALITY AS wanted(symbol,position)
		LEFT JOIN LATERAL (
			SELECT price_usd,minute,updated_at FROM quote_prices
			WHERE symbol=wanted.symbol ORDER BY minute DESC LIMIT 1
		) latest ON true
		LEFT JOIN quote_prices previous ON previous.symbol=wanted.symbol
			AND previous.minute=latest.minute-interval '24 hours'
		ORDER BY wanted.position`, symbols)
	if err != nil {
		return nil, fmt.Errorf("store: API quote prices: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (APIQuotePrice, error) {
		var p APIQuotePrice
		err := r.Scan(&p.Symbol, &p.PriceUSD, &p.Change24hPct, &p.UpdatedAt)
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("store: API quote prices: %w", err)
	}
	return out, nil
}
