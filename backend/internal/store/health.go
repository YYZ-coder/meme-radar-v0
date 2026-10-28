package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const healthChainsSQL = `WITH configured AS (
  SELECT name::chain_id AS chain, window_blocks, ord FROM unnest($1::text[], $2::bigint[]) WITH ORDINALITY AS c(name,window_blocks,ord)
 ), cursors AS (
  SELECT c.*, s.head, s.anchor, s.position, s.last_block_time, s.last_error_kind, s.last_error, s.last_error_at,
   CASE WHEN c.window_blocks>0 THEN greatest(s.head-c.window_blocks,0) ELSE coalesce(s.anchor,0) END AS lower_bound
  FROM configured c LEFT JOIN stream_cursors s ON s.chain=c.chain AND s.stream='forward'
 ) SELECT c.chain::text,c.head,coalesce(c.anchor,0),coalesce(c.lower_bound,0),c.position,
  CASE WHEN c.head IS NOT NULL THEN greatest(c.head-c.position,0) END,
  trunc(extract(epoch FROM ($3::timestamptz-c.last_block_time)))::bigint,
  (SELECT min(greatest(r.from_block,c.lower_bound)) FROM coverage_ranges r WHERE r.chain=c.chain AND r.to_block>=c.lower_bound AND r.from_block<=c.head),
  coalesce(g.total,0),coalesce(g.reasons,'{}'::jsonb),c.last_error_kind,c.last_error,c.last_error_at
 FROM cursors c LEFT JOIN LATERAL (
  SELECT sum(n)::bigint AS total,jsonb_object_agg(reason,n) AS reasons FROM (
   SELECT reason,sum(open_count)::bigint AS n FROM health_gap_counts WHERE chain=c.chain GROUP BY reason HAVING sum(open_count)<>0
  ) counts
 ) g ON true ORDER BY c.ord`

const healthParsersSQL = `WITH progress AS MATERIALIZED (
  SELECT s.chain,s.version,s.decoders,
   (SELECT block_num FROM chain_blocks b
    WHERE b.chain=s.chain AND b.parsed_version IS NOT NULL
    ORDER BY block_num DESC LIMIT 1) AS position,
   (SELECT coalesce(sum(p.pending),0)::bigint FROM parser_pending_counts p
    WHERE p.chain=s.chain) AS lag_blocks
  FROM parser_status s WHERE s.chain=ANY($1::chain_id[])
 ) SELECT s.chain::text,d.module,s.version,s.position,s.lag_blocks,
 coalesce(f.failures,0),coalesce(f.quarantined,0)
 FROM progress s CROSS JOIN LATERAL unnest(s.decoders) WITH ORDINALITY d(module,ord)
 LEFT JOIN (
  SELECT f.chain,f.decoder,count(*) AS failures,
   count(*) FILTER (WHERE NOT EXISTS (SELECT 1 FROM parse_retries r WHERE r.failure_id=f.id AND r.done_at IS NULL)) AS quarantined
  FROM decode_failures f WHERE f.created_at >= $2::timestamptz-interval '24 hours' AND f.created_at < $2
  AND f.chain=ANY($1::chain_id[]) GROUP BY f.chain,f.decoder
 ) f ON f.chain=s.chain AND f.decoder=d.module
 WHERE s.chain=ANY($1::chain_id[]) ORDER BY s.chain,d.ord`

const healthFailuresSQL = `SELECT ` + healthFailureColumns + ` FROM decode_failures f ORDER BY f.created_at DESC,f.id DESC LIMIT 50`

// HealthChainConfig specifies a configured chain's history window in blocks.
type HealthChainConfig struct {
	Chain        string
	WindowBlocks int64
}

// ChainHealth is the ingest/coverage state at an observation time.
type ChainHealth struct {
	Chain                                                             string
	Head, ForwardPosition, BacklogBlocks, LagSeconds, HistoryPosition *int64
	Anchor, LowerBound, GapsOpen                                      int64
	GapsByReason                                                      map[string]int64
	ErrorKind, ErrorMessage                                           *string
	ErrorAt                                                           *time.Time
}

// HealthChains reads configured chains, including chains without a cursor.
func (s *Store) HealthChains(ctx context.Context, chains []HealthChainConfig, now time.Time) ([]ChainHealth, error) {
	names, windows := make([]string, len(chains)), make([]int64, len(chains))
	for i, c := range chains {
		names[i], windows[i] = c.Chain, c.WindowBlocks
	}
	rows, err := s.pool.Query(ctx, healthChainsSQL, names, windows, now)
	if err != nil {
		return nil, fmt.Errorf("store: health chains: %w", err)
	}
	return collectHealthChains(rows)
}

// SaveParserStatus records the parse role's enabled modules at startup.
func (s *Store) SaveParserStatus(ctx context.Context, chain string, version int, decoders []string, startedAt time.Time) error {
	if decoders == nil {
		decoders = []string{}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO parser_status(chain,version,decoders,started_at) VALUES ($1,$2,$3,$4)
 ON CONFLICT (chain) DO UPDATE SET version=excluded.version,decoders=excluded.decoders,started_at=excluded.started_at`, chain, version, decoders, startedAt)
	if err != nil {
		return fmt.Errorf("store: save parser status: %w", err)
	}
	return nil
}

// ParserHealth reports one enabled decoder's recent failures and chain progress.
type ParserHealth struct {
	Chain, Module                    string
	Version                          int
	Position                         *int64
	LagBlocks, Failures, Quarantined int64
}

// HealthParsers reads parser status for the configured chains.
func (s *Store) HealthParsers(ctx context.Context, chains []string, now time.Time) ([]ParserHealth, error) {
	rows, err := s.pool.Query(ctx, healthParsersSQL, chains, now)
	if err != nil {
		return nil, fmt.Errorf("store: health parsers: %w", err)
	}
	return collectHealthParsers(rows)
}

// FailureHealth is a decode failure with block retry history.
type FailureHealth struct {
	ID, BlockNum          int64
	TxIndex               int
	Chain, Decoder, Error string
	Attempts              int64
	Queued                bool
	CreatedAt             time.Time
}

const healthFailureColumns = `f.id,f.chain::text,f.block_num,f.tx_index,f.decoder,f.error,f.created_at,
 1+(SELECT count(*) FROM parse_retries r WHERE r.chain=f.chain AND r.block_num=f.block_num AND r.done_at IS NOT NULL) AS attempts,
 EXISTS (SELECT 1 FROM parse_retries r WHERE r.failure_id=f.id AND r.done_at IS NULL) AS queued`

func scanHealthFailure(r pgx.CollectableRow) (FailureHealth, error) {
	var f FailureHealth
	err := r.Scan(&f.ID, &f.Chain, &f.BlockNum, &f.TxIndex, &f.Decoder, &f.Error, &f.CreatedAt, &f.Attempts, &f.Queued)
	return f, err
}

// HealthFailures returns the newest 50 failures, with id breaking timestamp ties.
func (s *Store) HealthFailures(ctx context.Context) ([]FailureHealth, error) {
	rows, err := s.pool.Query(ctx, healthFailuresSQL)
	if err != nil {
		return nil, fmt.Errorf("store: health failures: %w", err)
	}
	return collectHealthFailures(rows)
}

// QueueParseRetry inserts an API request without modifying parser-owned facts.
// Concurrent requests for the same pending failure share the partial unique key.
func (s *Store) QueueParseRetry(ctx context.Context, id int64, now time.Time) (FailureHealth, error) {
	rows, err := s.pool.Query(ctx, `WITH failure AS MATERIALIZED (
  SELECT `+healthFailureColumns+` FROM decode_failures f WHERE f.id=$1
 ), queued AS (
  INSERT INTO parse_retries(chain,block_num,failure_id,requested_at)
  SELECT chain::chain_id,block_num,id,$2 FROM failure
  ON CONFLICT (failure_id) WHERE done_at IS NULL DO NOTHING
 ) SELECT * FROM failure`, id, now)
	if err != nil {
		return FailureHealth{}, fmt.Errorf("store: queue parse retry: %w", err)
	}
	f, err := pgx.CollectOneRow(rows, scanHealthFailure)
	if errors.Is(err, pgx.ErrNoRows) {
		return FailureHealth{}, ErrNotFound
	}
	if err != nil {
		return FailureHealth{}, fmt.Errorf("store: queue parse retry: %w", err)
	}
	f.Queued = true
	return f, nil
}

// ExecuteParseRetries is called only by parse. Resetting the blocks and marking
// requests executed commit atomically; a later decode error leaves normal
// unparsed work available for the next step, including across process restarts.
func (s *Store) ExecuteParseRetries(ctx context.Context, chain string, now time.Time) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `WITH pending AS MATERIALIZED (
  SELECT id,block_num FROM parse_retries WHERE chain=$1 AND done_at IS NULL ORDER BY id FOR UPDATE
 ), reset AS (
  UPDATE chain_blocks b SET parsed_version=NULL FROM (SELECT DISTINCT block_num FROM pending) p
  WHERE b.chain=$1 AND b.block_num=p.block_num
 ), completed AS (
  UPDATE parse_retries r SET done_at=$2 FROM pending p WHERE r.id=p.id RETURNING r.block_num
 ) SELECT DISTINCT block_num FROM completed ORDER BY block_num`, chain, now)
	if err != nil {
		return nil, fmt.Errorf("store: execute parse retries: %w", err)
	}
	blocks, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("store: execute parse retries: %w", err)
	}
	return blocks, nil
}

func collectHealthChains(rows pgx.Rows) ([]ChainHealth, error) {
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ChainHealth, error) {
		var c ChainHealth
		err := r.Scan(&c.Chain, &c.Head, &c.Anchor, &c.LowerBound, &c.ForwardPosition, &c.BacklogBlocks, &c.LagSeconds, &c.HistoryPosition, &c.GapsOpen, &c.GapsByReason, &c.ErrorKind, &c.ErrorMessage, &c.ErrorAt)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("store: health chains: %w", err)
	}
	return out, nil
}

func collectHealthParsers(rows pgx.Rows) ([]ParserHealth, error) {
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ParserHealth, error) {
		var p ParserHealth
		err := r.Scan(&p.Chain, &p.Module, &p.Version, &p.Position, &p.LagBlocks, &p.Failures, &p.Quarantined)
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("store: health parsers: %w", err)
	}
	return out, nil
}

func collectHealthFailures(rows pgx.Rows) ([]FailureHealth, error) {
	out, err := pgx.CollectRows(rows, scanHealthFailure)
	if err != nil {
		return nil, fmt.Errorf("store: health failures: %w", err)
	}
	return out, nil
}
