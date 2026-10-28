package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// InflowPagedSnapshot carries current metadata and the requested complete page.
// Count is capped at2001 to preserve the public2000-row truncation contract.
type InflowPagedSnapshot struct {
	InflowSnapshot
	Count int
}

// Current heads and membership share the initial repeatable-read transaction.
// Frozen pages select only retained version IDs and never follow new heads.
const inflowPageHeads = `WITH params AS (SELECT $3::boolean,$4::integer,$5::integer,$9::bigint[]),
 active AS MATERIALIZED (SELECT version FROM sm_lists WHERE status='active'),
 heads AS MATERIALIZED (
 SELECT h.chain,h.projection_version,h.rebuild_pending,h.list_version FROM projection_heads h
 WHERE NOT $6::boolean AND h.name='inflow' AND h."window"=$2 AND ($1='' OR h.chain::text=$1)
 UNION ALL SELECT c::chain_id,v,false,0::bigint FROM unnest($7::text[],$8::bigint[])x(c,v) WHERE $6::boolean
 ), ready AS MATERIALIZED (
 SELECT * FROM heads h WHERE $6::boolean OR (EXISTS(SELECT 1 FROM active)
 AND NOT EXISTS(SELECT 1 FROM heads bad CROSS JOIN active a WHERE bad.rebuild_pending OR bad.list_version<>a.version))
 )`

const inflowPageCountSQL = inflowPageHeads + ` SELECT count(*) FROM (
 SELECT 1 FROM ready h CROSS JOIN LATERAL (
 SELECT 1 FROM inflow_rows r WHERE r.projection_version=h.projection_version AND r.chain=h.chain AND r."window"=$2 OFFSET 0
 )r LIMIT 2001)limited`

// Match Go's float64 integer ordering and canonical chain/address text ties.
// Only full values of the requested page cross the network, not every key.
// Partition the leading score/chain keys before rendering address ties. Groups
// outside this page never need base58 conversion. Scores originate in bigint,
// so -Infinity is a safe, non-colliding NULL sentinel for the hash join only.
const inflowTradePageSQL = inflowPageHeads + `, cached_heads AS MATERIALIZED (
 SELECT cardinality($9::bigint[])=(SELECT count(*) FROM heads)
 AND NOT EXISTS(SELECT 1 FROM heads WHERE NOT(projection_version=ANY($9::bigint[]))) AS matched
 ), page_heads AS MATERIALIZED (
 SELECT * FROM ready WHERE NOT(SELECT matched FROM cached_heads)
 ), candidates AS MATERIALIZED (
 SELECT r.chain,r.token_address,h.projection_version,r.sm_trades::float8 AS score
 FROM page_heads h CROSS JOIN LATERAL (
 SELECT r.chain,r.token_address,r.sm_trades FROM inflow_rows r
 WHERE r.projection_version=h.projection_version AND r.chain=h.chain AND r."window"=$2 OFFSET 0
 )r
 ), group_sizes AS (
 SELECT chain,score,count(*) AS n FROM candidates GROUP BY chain,score
 ), positioned AS (
 SELECT *,coalesce(sum(n) OVER(ORDER BY score DESC NULLS LAST,chain::text COLLATE "C"
 ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0)::bigint AS starts_at FROM group_sizes
 ), selected_groups AS MATERIALIZED (
 SELECT * FROM positioned WHERE starts_at<least($4+$5,2000) AND starts_at+n>$4
 ), page_keys AS (
 SELECT c.*,addr(c.chain,c.token_address) COLLATE "C" AS display_address
 FROM candidates c JOIN selected_groups g ON c.chain=g.chain
 AND coalesce(c.score,'-Infinity'::float8)=coalesce(g.score,'-Infinity'::float8)
 ORDER BY c.score DESC NULLS LAST,c.chain::text COLLATE "C",display_address
 LIMIT least($5,greatest(0,2000-$4))
 OFFSET greatest(0,$4-(SELECT coalesce(min(starts_at),$4) FROM selected_groups))
 ) SELECT ` + inflowPayloadColumns + `
 FROM page_keys k CROSS JOIN LATERAL (
 SELECT r.* FROM inflow_rows r WHERE r.projection_version=k.projection_version AND r.chain=k.chain
 AND r."window"=$2 AND r.token_address=k.token_address OFFSET 0
 )r` + inflowSeriesJoin + `
 ORDER BY k.score DESC NULLS LAST,k.chain::text COLLATE "C",k.display_address COLLATE "C"`

// ReadInflowTradePages returns the descending smart-trade first page with
// current membership and heads in one network exchange, without value priming.
func (s *Store) ReadInflowTradePages(ctx context.Context, chain, window string, native bool, limit int) (InflowPagedSnapshot, error) {
	key := inflowPageShape{chain: chain, window: window, native: native, limit: limit}
	cached, haveCached := s.inflowPages.get(key)
	knownVersions := make([]int64, 0, len(cached.Heads))
	if haveCached {
		for _, h := range cached.Heads {
			knownVersions = append(knownVersions, h.Version)
		}
	}
	return hedgeRead(ctx, s, func(ctx context.Context) (InflowPagedSnapshot, error) {
		out := InflowPagedSnapshot{InflowSnapshot: InflowSnapshot{Rows: []InflowDataRow{}}}
		args := []any{chain, window, native, 0, limit, false, []string{}, []int64{}, knownVersions}
		var batch pgx.Batch
		batch.Queue("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY")
		batch.Queue(inflowHeadsSQL, chain, window, false, []string{})
		batch.Queue(inflowPageCountSQL, args...)
		batch.Queue(inflowTradePageSQL, args...)
		batch.Queue("COMMIT")
		results := s.pool.SendBatch(ctx, &batch)
		defer results.Close()
		if _, err := results.Exec(); err != nil {
			return out, err
		}
		rows, err := results.Query()
		if err != nil {
			return out, err
		}
		pending := false
		for rows.Next() {
			var ch *string
			var at *time.Time
			var list, version *int64
			var waiting *bool
			if err = rows.Scan(&out.ListVersion, &ch, &at, &list, &waiting, &version); err != nil {
				rows.Close()
				return out, err
			}
			if ch != nil {
				out.Heads = append(out.Heads, InflowHead{Chain: *ch, Window: window, AsOf: at.UTC(), Version: *version})
				pending = pending || *waiting || *list != out.ListVersion
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		if err = results.QueryRow().Scan(&out.Count); err != nil {
			return out, err
		}
		rows, err = results.Query()
		if err != nil {
			return out, err
		}
		out.Rows, err = collectReadInflowRows(rows)
		if err != nil {
			return out, fmt.Errorf("store: inflow page: %w", err)
		}
		if _, err = results.Exec(); err != nil {
			return out, err
		}
		if err = results.Close(); err != nil {
			return out, err
		}
		if pending {
			return out, ErrProjectionPending
		}
		if haveCached && sameInflowPageHeads(out.Heads, cached.Heads) {
			if out.Count != cached.Count {
				return out, fmt.Errorf("store: immutable inflow publication count changed")
			}
			out.Rows = cached.Rows
		} else {
			s.inflowPages.put(key, out)
		}
		return out, nil
	})
}

// ReadInflowTradePage reads a later ordered page at retained immutable versions.
// Membership activation does not change an already-created query's contents.
func (s *Store) ReadInflowTradePage(ctx context.Context, window string, native bool, heads []InflowHead, offset, limit int) ([]InflowDataRow, error) {
	chains := make([]string, len(heads))
	versions := make([]int64, len(heads))
	for i, h := range heads {
		chains[i], versions[i] = h.Chain, h.Version
	}
	return hedgeRead(ctx, s, func(ctx context.Context) ([]InflowDataRow, error) {
		rows, err := s.pool.Query(ctx, inflowTradePageSQL, "", window, native, offset, limit, true, chains, versions, []int64{})
		if err != nil {
			return nil, err
		}
		return collectReadInflowRows(rows)
	})
}
