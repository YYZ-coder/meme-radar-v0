package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TokenDetailData contains the current identity, holdings and observed windows.
type TokenDetailData struct {
	ReadAt   time.Time
	Token    APITokenDetail
	Holdings APITokenHoldings
	AsOf     time.Time
	Windows  []WindowValues
	SameName []APISearchToken
}

// ReadTokenDetail pipelines the complete read in one repeatable-read batch.
// Every batch is closed before the connection is reused. Missing tokens and
// unknown decimals keep their existing response semantics.
func (s *Store) ReadTokenDetail(ctx context.Context, chain string, token []byte, native bool, now time.Time) (TokenDetailData, bool, error) {
	type result struct {
		data  TokenDetailData
		found bool
	}
	r, err := hedgeAPIRead(ctx, s, func(attempt context.Context) (result, error) {
		data, found, err := s.readTokenDetail(attempt, chain, token, native, now)
		return result{data, found}, err
	})
	return r.data, r.found, err
}

// detailWindowQuery derives the same minute cutoff inside the transaction.
// Shared SQL templates retain the standalone window semantics.
func detailWindowQuery(native bool) string {
	cutoff := strings.ReplaceAll(chainAsOfSQL, "$2", "$3")
	query := strings.ReplaceAll(tokenWindowQuery(native), "$3", "(SELECT at FROM detail_cutoff)")
	prefix := "WITH detail_cutoff AS MATERIALIZED (SELECT coalesce((" + cutoff + "),date_trunc('minute',$3::timestamptz)) AS at), "
	return strings.Replace(query, "WITH ", prefix, 1)
}

// A bounded symbol probe selects indexed matching for small groups and market
// order for large groups. It does not cap candidates or change peer semantics;
// the server-derived symbol cannot use a client-bound selectivity estimate.
const detailSameNameSQL = `WITH target AS MATERIALIZED (
 SELECT lower(symbol) AS symbol FROM token_info WHERE chain=$1 AND address=$2
 ), probe AS MATERIALIZED (
 SELECT t.chain,t.address FROM tokens t
 WHERE lower(t.symbol)=(SELECT symbol FROM target) AND NOT(t.chain=$1::chain_id AND t.address=$2::bytea)
 LIMIT 1001
 ), small_matches AS MATERIALIZED (
 SELECT * FROM probe WHERE (SELECT count(*) FROM probe)<=1000
 ), recent AS MATERIALIZED (
 (SELECT m.chain,m.address,m.last_trade_at FROM small_matches p
 JOIN token_market m ON m.chain=p.chain AND m.address=p.address
 WHERE m.last_trade_at IS NOT NULL
 ORDER BY m.last_trade_at DESC NULLS LAST,m.chain,m.address LIMIT 10)
 UNION ALL
 (SELECT m.chain,m.address,m.last_trade_at FROM token_market m
 CROSS JOIN LATERAL(SELECT 1 FROM tokens t WHERE t.chain=m.chain AND t.address=m.address
  AND lower(t.symbol)=(SELECT symbol FROM target) OFFSET 0)matching
 WHERE (SELECT count(*) FROM probe)>1000 AND m.last_trade_at IS NOT NULL
 AND NOT(m.chain=$1::chain_id AND m.address=$2::bytea)
 ORDER BY m.last_trade_at DESC NULLS LAST,m.chain,m.address LIMIT 10)
 ORDER BY last_trade_at DESC NULLS LAST,chain,address LIMIT 10
 ), untraded AS (
 (SELECT p.chain,p.address,NULL::timestamptz AS last_trade_at FROM small_matches p
 LEFT JOIN token_market m ON m.chain=p.chain AND m.address=p.address
 WHERE m.last_trade_at IS NULL AND (SELECT count(*) FROM recent)<10
 ORDER BY p.chain,p.address LIMIT 10)
 UNION ALL
 (SELECT t.chain,t.address,NULL::timestamptz AS last_trade_at FROM tokens t
 LEFT JOIN token_market m ON m.chain=t.chain AND m.address=t.address
 WHERE (SELECT count(*) FROM probe)>1000 AND lower(t.symbol)=(SELECT symbol FROM target)
 AND NOT(t.chain=$1::chain_id AND t.address=$2::bytea) AND m.last_trade_at IS NULL
 AND (SELECT count(*) FROM recent)<10 ORDER BY t.chain,t.address LIMIT 10)
 ), peers AS (
 SELECT * FROM recent UNION ALL SELECT * FROM untraded
 ORDER BY last_trade_at DESC NULLS LAST,chain,address LIMIT 10
 ) SELECT t.chain::text,t.address,t.symbol,t.name,t.launch_platform,t.launch_at,
 trim_scale(m.market_cap_usd)::text,floor(extract(epoch FROM($3::timestamptz-t.launch_at)))::bigint,k.last_trade_at
 FROM peers k JOIN token_info t ON t.chain=k.chain AND t.address=k.address
 LEFT JOIN token_market m ON m.chain=k.chain AND m.address=k.address
 ORDER BY k.last_trade_at DESC NULLS LAST,k.chain,k.address`

func (s *Store) readTokenDetail(ctx context.Context, chain string, token []byte, native bool, now time.Time) (TokenDetailData, bool, error) {
	var out TokenDetailData
	// A repeatable-read snapshot makes the cutoff and symbol subqueries agree
	// with their returned header values, without waiting for another wire trip.
	var batch pgx.Batch
	batch.Queue("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY")
	batch.Queue("SET LOCAL plan_cache_mode = force_custom_plan")
	batch.Queue(observationTimeSQL)
	batch.Queue(apiTokenSQL, chain, token, native)
	batch.Queue(chainAsOfSQL, chain, now.UTC())
	batch.Queue(apiTokenHoldingsSQL, chain, token)
	batch.Queue(detailWindowQuery(native), chain, token, now.UTC(), []int64{300, 3600, 14400, 86400}, native)
	batch.Queue(detailSameNameSQL, chain, token, now.UTC())
	batch.Queue("COMMIT")
	results := s.apiReadPool().SendBatch(ctx, &batch)
	defer results.Close()
	if _, err := results.Exec(); err != nil {
		return out, false, fmt.Errorf("store: detail begin: %w", err)
	}
	if _, err := results.Exec(); err != nil {
		return out, false, fmt.Errorf("store: detail planning: %w", err)
	}
	var found bool
	var err error
	if err = results.QueryRow().Scan(&out.ReadAt); err != nil {
		return out, false, fmt.Errorf("store: detail observation time: %w", err)
	}
	out.Token, found, err = scanAPIToken(results.QueryRow())
	if err != nil {
		return out, false, err
	}
	if !found || out.Token.Decimals == nil {
		return out, found, results.Close()
	}
	var cutoff *time.Time
	if err = results.QueryRow().Scan(&cutoff); err != nil {
		return out, false, fmt.Errorf("store: detail cutoff: %w", err)
	}
	out.AsOf = now.UTC().Truncate(time.Minute)
	if cutoff != nil {
		out.AsOf = cutoff.UTC()
	}
	out.Holdings, err = scanAPITokenHoldings(results.QueryRow())
	if err != nil {
		return out, false, err
	}
	rows, err := results.Query()
	if err != nil {
		return out, false, fmt.Errorf("store: detail windows: %w", err)
	}
	out.Windows, err = collectTokenWindows(rows)
	if err != nil {
		return out, false, err
	}
	rows, err = results.Query()
	if err != nil {
		return out, false, fmt.Errorf("store: detail same name: %w", err)
	}
	out.SameName, err = collectAPISameName(rows)
	if err != nil {
		return out, false, err
	}
	if _, err = results.Exec(); err != nil {
		return out, false, fmt.Errorf("store: detail commit: %w", err)
	}
	if err = results.Close(); err != nil {
		return out, false, fmt.Errorf("store: detail batch: %w", err)
	}
	return out, true, nil
}
