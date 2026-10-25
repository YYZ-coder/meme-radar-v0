package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// APITokenPrecision returns parse/enrich precision and whether the token exists.
func (s *Store) APITokenPrecision(ctx context.Context, chain string, token []byte) (*int16, bool, error) {
	var decimals *int16
	err := s.pool.QueryRow(ctx, `SELECT supply_decimals FROM token_info WHERE chain=$1 AND address=$2`, chain, token).Scan(&decimals)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: API token precision: %w", err)
	}
	return decimals, true, nil
}
