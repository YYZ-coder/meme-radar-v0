package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/meme-radar/radar/internal/parse"
)

// BalanceBlock is the balance-only decode result of one raw block (D154).
type BalanceBlock struct {
	Chain   string
	Num     uint64
	Time    time.Time
	Version int
	Deltas  []parse.Balance
	Hash    []byte // selected raw block hash; nil for direct fact fixtures
}

// ApplyBalances replaces only balance facts and their independent progress,
// atomically for the batch. It never changes main-parser facts or progress.
func (s *Store) ApplyBalances(ctx context.Context, blocks []BalanceBlock) error {
	if len(blocks) == 0 {
		return nil
	}
	chain := blocks[0].Chain
	nums := make([]int64, len(blocks))
	for i, p := range blocks {
		if p.Chain != chain {
			return fmt.Errorf("store: mixed balance chains")
		}
		nums[i] = int64(p.Num)
		if err := s.ensurePartition(ctx, "balance_deltas", p.Time); err != nil {
			return err
		}
	}
	var b pgx.Batch
	// Serialize balance writers and their compute watermark, including writers
	// that began before another batch committed (transaction timestamps differ).
	b.Queue(`SELECT pg_advisory_xact_lock(7103,hashtext($1))`, chain)
	b.Queue(`SELECT block_num FROM chain_blocks WHERE chain=$1 AND block_num=ANY($2) ORDER BY block_num FOR UPDATE`, chain, nums)
	updates := map[int]bool{}
	for _, p := range blocks {
		updates[b.Len()] = true
		b.Queue(`UPDATE chain_blocks SET balance_version=$3,balance_parsed_at=clock_timestamp()
            WHERE chain=$1 AND block_num=$2 AND block_time=$4 AND status<>'skipped'
            AND parsed_version IS NOT NULL AND ($5::bytea IS NULL OR block_hash=$5)`, chain, int64(p.Num), p.Version, p.Time, p.Hash)
		b.Queue(`DELETE FROM balance_deltas WHERE chain=$1 AND block_num=$2`, chain, int64(p.Num))
		txs, ixs := make([]int32, len(p.Deltas)), make([]int32, len(p.Deltas))
		tokens, owners := make([][]byte, len(p.Deltas)), make([][]byte, len(p.Deltas))
		amounts := make([]string, len(p.Deltas))
		for i, d := range p.Deltas {
			txs[i], ixs[i], tokens[i], owners[i], amounts[i] = int32(d.Tx), int32(d.Ix), d.Token, d.Owner, d.Delta
		}
		b.Queue(`INSERT INTO balance_deltas (block_time,chain,block_num,tx_index,ix_index,token_address,owner,delta)
            SELECT $1,$2,$3,t,ix,token,owner,amount::numeric FROM unnest($4::int[],$5::int[],$6::bytea[],$7::bytea[],$8::text[]) AS x(t,ix,token,owner,amount)`,
			p.Time, chain, int64(p.Num), txs, ixs, tokens, owners, amounts)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: balance begin: %w", err)
	}
	defer tx.Rollback(ctx)
	results := tx.SendBatch(ctx, &b)
	for i := 0; i < b.Len(); i++ {
		tag, err := results.Exec()
		if err != nil {
			results.Close()
			return fmt.Errorf("store: balance write: %w", err)
		}
		if updates[i] && tag.RowsAffected() != 1 {
			results.Close()
			return fmt.Errorf("store: balance block changed or missing: %w", ErrNotFound)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("store: balance write: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: balance commit: %w", err)
	}
	return nil
}

// UnbalancedBlocks returns retained raw blocks whose main parse completed,
// newest first. It yields completely while over 1000 main blocks await parsing.
func (s *Store) UnbalancedBlocks(ctx context.Context, chain string, version, limit int) ([]Block, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.block_num,c.block_time,c.status,c.block_hash,r.payload FROM chain_blocks c
        JOIN raw_blocks r ON r.chain=c.chain AND r.block_num=c.block_num AND r.block_hash=c.block_hash AND r.block_time=c.block_time
        WHERE c.chain=$1 AND c.status<>'skipped' AND c.parsed_version IS NOT NULL
        AND (c.balance_version IS NULL OR c.balance_version<$2)
        AND NOT EXISTS (SELECT 1 FROM chain_blocks p WHERE p.chain=$1 AND p.status<>'skipped' AND p.parsed_version IS NULL OFFSET 1000)
        ORDER BY c.block_num DESC LIMIT $3`, chain, version, limit)
	if err != nil {
		return nil, fmt.Errorf("store: unbalanced blocks: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Block, error) {
		var b Block
		var n int64
		var compressed []byte
		if err := r.Scan(&n, &b.Time, &b.Status, &b.Hash, &compressed); err != nil {
			return b, err
		}
		b.Num = uint64(n)
		var err error
		b.Payload, err = decoder.DecodeAll(compressed, nil)
		return b, err
	})
}

// BalanceToken is a tracked address with its first chain observation.
type BalanceToken struct {
	Address   []byte
	FirstSeen time.Time
	UpdatedAt time.Time
}

// BalanceTokens refreshes tracking by first_seen_at, plus tokens first seen
// in the current batch. The latter catches newly parsed historical blocks
// whose chain timestamps precede the incremental refresh watermark. updated_at
// catches late token inserts before decoding their newer blocks; parse is the
// sole token writer and commits in order under its per-chain session lock.
func (s *Store) BalanceTokens(ctx context.Context, chain string, since, updatedSince *time.Time, nums []int64) ([]BalanceToken, error) {
	rows, err := s.pool.Query(ctx, `SELECT address,first_seen_at,updated_at FROM tokens
        WHERE chain=$1 AND ($2::timestamptz IS NULL OR first_seen_at>=$2 OR updated_at>=$4::timestamptz
            OR created_block=ANY($3::bigint[])
            OR first_seen_at IN (SELECT block_time FROM chain_blocks WHERE chain=$1 AND block_num=ANY($3)))`, chain, since, nums, updatedSince)
	if err != nil {
		return nil, fmt.Errorf("store: balance tokens: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (BalanceToken, error) {
		var t BalanceToken
		err := r.Scan(&t.Address, &t.FirstSeen, &t.UpdatedAt)
		return t, err
	})
}

// RefreshBalances corrects previously applied block deltas through the journal
// and advances the independent balance watermark in the same transaction.
func (s *Store) RefreshBalances(ctx context.Context, chain string, now time.Time, version int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: balances begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7103,hashtext($1))`, chain); err != nil {
		return err
	}
	var nums []int64
	var mark *time.Time
	if err := tx.QueryRow(ctx, `WITH changed AS (
        SELECT block_num,balance_parsed_at FROM chain_blocks WHERE chain=$1
        AND balance_parsed_at>coalesce((SELECT at FROM compute_marks WHERE chain=$1 AND name='balances'),'-infinity'::timestamptz)
    ), affected AS (
        SELECT block_num FROM changed UNION
        SELECT j.block_num FROM balance_journal j LEFT JOIN chain_blocks c USING(chain,block_num)
        WHERE j.chain=$1 AND c.balance_version IS NULL
    ) SELECT coalesce(array_agg(block_num),'{}'),(SELECT max(balance_parsed_at) FROM changed) FROM affected`, chain).Scan(&nums, &mark); err != nil {
		return fmt.Errorf("store: balance changes: %w", err)
	}
	if len(nums) == 0 {
		if err := refreshHolders(ctx, tx, chain, version, now); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var b pgx.Batch
	b.Queue(`WITH changes AS (
        SELECT token_address,owner,-delta AS delta FROM balance_journal WHERE chain=$1 AND block_num=ANY($2)
        UNION ALL SELECT d.token_address,d.owner,d.delta FROM balance_deltas d JOIN chain_blocks c USING(chain,block_num)
        WHERE d.chain=$1 AND d.block_num=ANY($2) AND c.balance_version IS NOT NULL
    ) INSERT INTO token_balances (chain,token_address,owner,balance,updated_at)
        SELECT $1,token_address,owner,sum(delta),$3 FROM changes GROUP BY token_address,owner
        ON CONFLICT (chain,token_address,owner) DO UPDATE SET balance=token_balances.balance+EXCLUDED.balance,updated_at=EXCLUDED.updated_at`, chain, nums, now)
	b.Queue(`DELETE FROM balance_journal WHERE chain=$1 AND block_num=ANY($2)`, chain, nums)
	b.Queue(`INSERT INTO balance_journal (chain,block_num,token_address,owner,delta)
        SELECT d.chain,d.block_num,d.token_address,d.owner,sum(d.delta) FROM balance_deltas d JOIN chain_blocks c USING(chain,block_num)
        WHERE d.chain=$1 AND d.block_num=ANY($2) AND c.balance_version IS NOT NULL
        GROUP BY d.chain,d.block_num,d.token_address,d.owner HAVING sum(d.delta)<>0`, chain, nums)
	if mark != nil {
		b.Queue(`INSERT INTO compute_marks (chain,name,at) VALUES ($1,'balances',$2)
        ON CONFLICT (chain,name) DO UPDATE SET at=EXCLUDED.at`, chain, mark)
	}
	if err := tx.SendBatch(ctx, &b).Close(); err != nil {
		return fmt.Errorf("store: accumulate balances: %w", err)
	}
	if err := refreshHolders(ctx, tx, chain, version, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: balances commit: %w", err)
	}
	return nil
}

// pruneBalanceLedger forgets expired correction history without retracting
// accumulated balances. Journal and ledger expiry are atomic with compute.
func (s *Store) pruneBalanceLedger(ctx context.Context, rawBefore, ledgerBefore time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7103,hashtext(chain)) FROM
        (SELECT unnest(enum_range(NULL::chain_id))::text AS chain ORDER BY chain) c`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid
        WHERE i.inhparent='balance_deltas'::regclass`)
	if err != nil {
		return err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	var b pgx.Batch
	for _, name := range names {
		day, err := time.Parse("balance_deltas_20060102", name)
		if err == nil && !day.Add(24*time.Hour).After(ledgerBefore) {
			b.Queue("DROP TABLE IF EXISTS " + pgx.Identifier{name}.Sanitize())
			s.days.Delete("balance_deltas" + day.Format("20060102"))
		}
	}
	b.Queue(`DELETE FROM balance_journal j USING chain_blocks c
        WHERE j.chain=c.chain AND j.block_num=c.block_num AND c.block_time<$1`, maxTime(rawBefore, ledgerBefore))
	b.Queue(`DELETE FROM chain_blocks WHERE block_time<$1`, ledgerBefore)
	if err := tx.SendBatch(ctx, &b).Close(); err != nil {
		return fmt.Errorf("store: prune balance history: %w", err)
	}
	return tx.Commit(ctx)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// BalanceRows counts retained owner/token rows for the backlog gauge.
func (s *Store) BalanceRows(ctx context.Context, chain string) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM token_balances WHERE chain=$1`, chain).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: balance row count: %w", err)
	}
	return n, nil
}

// refreshHolders runs under the same balance transaction/lock as accumulation,
// so freshly parsed but not yet accumulated blocks cannot produce false full.
func refreshHolders(ctx context.Context, tx pgx.Tx, chain string, version int, now time.Time) error {
	_, err := tx.Exec(ctx, `WITH chain_state AS MATERIALIZED (
        SELECT max(block_num) AS current_block,max(block_num) FILTER (WHERE status<>'skipped'
            AND (parsed_version IS NULL OR balance_version IS NULL OR balance_version<$2)) AS pending_max
        FROM chain_blocks WHERE chain=$1
    ), totals AS (
        SELECT token_address,count(*) AS records,count(*) FILTER (WHERE balance>0 AND is_holder_owner(chain,owner)) AS holders
        FROM token_balances WHERE chain=$1 GROUP BY token_address
    ), state AS (
        SELECT t.address,t.launch_at,coalesce(b.holders,0)::int AS holders,d.since,
            coalesce(b.records,0)>0 OR d.since IS NOT NULL AS recorded,
            t.created_block IS NOT NULL AND (cs.pending_max IS NULL OR t.created_block>cs.pending_max)
            AND EXISTS (SELECT 1 FROM coverage_ranges cr WHERE cr.chain=$1 AND cr.from_block<=t.created_block AND cr.to_block>=cs.current_block) AS complete
        FROM tokens t CROSS JOIN chain_state cs LEFT JOIN totals b ON b.token_address=t.address
        LEFT JOIN LATERAL (SELECT min(block_time) AS since FROM balance_deltas d
            WHERE d.chain=t.chain AND d.token_address=t.address) d ON true WHERE t.chain=$1
    ) INSERT INTO token_holder_state (chain,token_address,holders,coverage,since,as_of)
        SELECT $1,address,holders,CASE WHEN complete THEN 'full' WHEN recorded THEN 'partial' ELSE 'unknown' END,
            CASE WHEN complete THEN launch_at ELSE since END,
            coalesce((SELECT at FROM compute_marks WHERE chain=$1 AND name='balances'),$3)
        FROM state ON CONFLICT (chain,token_address) DO UPDATE SET holders=EXCLUDED.holders,
            coverage=EXCLUDED.coverage,since=EXCLUDED.since,as_of=EXCLUDED.as_of
        WHERE (token_holder_state.holders,token_holder_state.coverage,token_holder_state.since,token_holder_state.as_of)
            IS DISTINCT FROM (EXCLUDED.holders,EXCLUDED.coverage,EXCLUDED.since,EXCLUDED.as_of)`, chain, version, now)
	if err != nil {
		return fmt.Errorf("store: holder state: %w", err)
	}
	return nil
}

// BalanceViolation is an atomic-unit supply mismatch for a fully covered EVM token.
type BalanceViolation struct {
	Token            []byte
	Actual, Expected string
}

// BalanceViolations checks creation supply less excluded burn balances. Unknown
// supply and partial/unknown coverage are deliberately outside this invariant.
func (s *Store) BalanceViolations(ctx context.Context, chain string) ([]BalanceViolation, error) {
	rows, err := s.pool.Query(ctx, `SELECT h.token_address,trim_scale(v.actual)::text,trim_scale(t.supply-v.burned)::text
        FROM token_holder_state h JOIN token_info t ON t.chain=h.chain AND t.address=h.token_address
        CROSS JOIN LATERAL (SELECT coalesce(sum(balance) FILTER(WHERE is_holder_owner(b.chain,b.owner)),0) AS actual,
            coalesce(sum(balance) FILTER(WHERE NOT is_holder_owner(b.chain,b.owner)),0) AS burned
            FROM token_balances b WHERE b.chain=h.chain AND b.token_address=h.token_address) v
        WHERE h.chain=$1 AND h.chain<>'solana' AND h.coverage='full' AND t.supply IS NOT NULL AND v.actual<>t.supply-v.burned`, chain)
	if err != nil {
		return nil, fmt.Errorf("store: balance invariant: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (BalanceViolation, error) {
		var v BalanceViolation
		err := r.Scan(&v.Token, &v.Actual, &v.Expected)
		return v, err
	})
}
