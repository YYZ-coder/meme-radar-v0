package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// WarmAPIReads prepares protocol statements without executing dummy queries or
// priming responses. Health/detail use a separate bounded read-only pool, whose
// replacement connections are prepared before they enter service.
func (s *Store) WarmAPIReads(ctx context.Context) error {
	inflow := []string{"BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", inflowHeadsSQL, inflowRowsSQL, inflowSelectionSQL, inflowPinnedRowsSQL, inflowPageCountSQL, inflowTradePageSQL, "COMMIT"}
	if err := holdWarmConnections(ctx, s.pool, min(4, int(s.pool.Config().MaxConns)), func(ctx context.Context, c *pgx.Conn) error { return prepareAPIStatements(ctx, c, inflow) }); err != nil {
		return err
	}
	return s.initAPIReadPool(ctx)
}

func apiDetailHealthStatements() []string {
	return []string{"BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", "SET LOCAL plan_cache_mode = force_custom_plan", observationTimeSQL,
		apiTokenSQL, apiTokenHoldingsSQL, chainAsOfSQL, detailWindowQuery(false), detailWindowQuery(true), detailSameNameSQL,
		healthChainsSQL, healthParsersSQL, healthFailureReadSQL, "COMMIT"}
}

func prepareAPIStatements(ctx context.Context, conn *pgx.Conn, queries []string) error {
	for _, sql := range queries {
		// pgx supports using SQL as the local prepared-statement key; it hashes the
		// server name. Existing Query/SendBatch calls then reuse that description.
		if _, err := conn.Prepare(ctx, sql, sql); err != nil {
			return fmt.Errorf("store: prepare API read: %w", err)
		}
	}
	return nil
}
