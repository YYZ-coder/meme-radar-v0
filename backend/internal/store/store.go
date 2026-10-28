// Package store is the PostgreSQL persistence of the ingest layer: full raw
// blocks, the per-block ledger (chain_blocks), the forward cursor and gaps.
package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/klauspost/compress/zstd"

	"github.com/meme-radar/radar/migrations"
)

// Block statuses in chain_blocks.
const (
	StatusPending   = "pending"
	StatusConfirmed = "confirmed"
	StatusSkipped   = "skipped"
)

// Gap reasons in stream_gaps.
const (
	GapStartup     = "startup"
	GapRateLimited = "rate_limited"
)

// ErrNotFound reports a missing block, cursor or raw payload.
var ErrNotFound = errors.New("store: not found")

// ErrLocked reports that another process holds the chain's ingest lock.
var ErrLocked = errors.New("store: chain is locked by another ingest")

// Store is safe for concurrent use; Close releases its pool.
type Store struct {
	pool *pgxpool.Pool
	// days caches daily partitions known to exist (table + "YYYYMMDD").
	days           sync.Map
	readHedges     readHedgeLimiter
	apiHedges      readHedgeLimiter
	inflowCache    inflowReadCache
	inflowPages    inflowPageReadCache
	healthFailures healthFailureReadCache
	apiReads       atomic.Pointer[pgxpool.Pool]
	apiReadMu      sync.Mutex
	closed         bool
}

var (
	encoder, _ = zstd.NewWriter(nil)
	decoder, _ = zstd.NewReader(nil)
)

// Open connects to the database at url.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases all owned connections after callers have stopped serving.
func (s *Store) Close() {
	s.apiReadMu.Lock()
	s.closed = true
	p := s.apiReads.Swap(nil)
	s.apiReadMu.Unlock()
	if p != nil {
		p.Close()
	}
	s.pool.Close()
}

// migrateLock serialises concurrent Migrate calls across processes.
const migrateLock = 7_100_001

// Migrate applies pending migrations from the migrations package in one
// transaction, recording applied file names in schema_migrations.
func (s *Store) Migrate(ctx context.Context) error {
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return fmt.Errorf("store: list migrations: %w", err)
	}
	sort.Strings(names)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrateLock); err != nil {
		return fmt.Errorf("store: migrate lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	for _, name := range names {
		var done bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)", name).Scan(&done); err != nil {
			return fmt.Errorf("store: migrate %s: %w", name, err)
		}
		if done {
			continue
		}
		sql, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return fmt.Errorf("store: migrate %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("store: migrate %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (name) VALUES ($1)", name); err != nil {
			return fmt.Errorf("store: migrate %s: %w", name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: migrate commit: %w", err)
	}
	return nil
}

// Block is one processed block. Payload is the uncompressed source response
// and is ignored for skipped blocks, which carry no hash or time either.
type Block struct {
	Num        uint64
	Hash       []byte
	ParentHash []byte
	Time       time.Time
	Status     string
	TxCount    int
	Payload    []byte
	Source     string
}

// Cursor is the forward stream position of a chain.
type Cursor struct {
	Anchor   uint64 // first block of the stream
	Position uint64 // highest processed block
	Head     uint64 // chain head seen when Position was committed
}

// Gap is an unprocessed block range [From, To].
type Gap struct {
	ID       int64
	From, To uint64
	Reason   string
}

// Commit is written in one transaction.
type Commit struct {
	Chain   string
	Block   Block
	Forward *Cursor // set by the forward stream
	NewGap  *Gap    // ID is ignored
	// FillGap is the ID of the open gap whose top block is Block.Num.
	FillGap int64
}

// Commit writes the block, its raw payload and the cursor and gap changes
// atomically. Rewriting a block replaces the previous row.
func (s *Store) Commit(ctx context.Context, c Commit) error {
	return s.CommitBlocks(ctx, []Commit{c})
}

// CommitBlocks writes several commits in one transaction (one round trip
// for all statements); an error in any of them writes none.
func (s *Store) CommitBlocks(ctx context.Context, cs []Commit) error {
	if len(cs) == 0 {
		return nil
	}
	for _, c := range cs {
		if c.Block.Status != StatusSkipped {
			if err := s.ensurePartition(ctx, "raw_blocks", c.Block.Time); err != nil {
				return err
			}
		}
	}
	var batch pgx.Batch
	fills := map[int]Commit{} // statement index of each gap fill
	for _, c := range cs {
		if i := queueCommit(&batch, c); i >= 0 {
			fills[i] = c
		}
	}
	first, last := cs[0], cs[len(cs)-1]
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: commit %s %d…%d: %w", first.Chain, first.Block.Num, last.Block.Num, err)
	}
	defer tx.Rollback(ctx)
	results := tx.SendBatch(ctx, &batch)
	for i := range batch.Len() {
		tag, err := results.Exec()
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23514" {
				// No partition: another process pruned it; recreate on retry.
				for _, c := range cs {
					s.days.Delete("raw_blocks" + c.Block.Time.UTC().Truncate(24*time.Hour).Format("20060102"))
				}
			}
			results.Close()
			return fmt.Errorf("store: commit %s %d…%d: %w", first.Chain, first.Block.Num, last.Block.Num, err)
		}
		if c, ok := fills[i]; ok && tag.RowsAffected() != 1 {
			results.Close()
			return fmt.Errorf("store: commit %s %d: block is not the top of open gap %d", c.Chain, c.Block.Num, c.FillGap)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("store: commit %s %d…%d: %w", first.Chain, first.Block.Num, last.Block.Num, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit %s %d…%d: %w", first.Chain, first.Block.Num, last.Block.Num, err)
	}
	return nil
}

// queueCommit adds c's statements to batch and returns the index of its gap
// fill statement, or -1.
func queueCommit(batch *pgx.Batch, c Commit) int {
	b := c.Block
	skipped := b.Status == StatusSkipped
	// Balance compute must not observe an ingest rewrite halfway through.
	batch.Queue(`SELECT pg_advisory_xact_lock(7103,hashtext($1))`, c.Chain)
	// All statements go in one batch: begin, batch and commit are the only
	// round trips, which dominate when the database is remote.
	batch.Queue("DELETE FROM raw_blocks WHERE chain = $1 AND block_num = $2", c.Chain, int64(b.Num))
	var hash, parent []byte
	var at *time.Time
	if !skipped {
		hash, parent, at = b.Hash, b.ParentHash, &b.Time
		batch.Queue(`INSERT INTO raw_blocks (block_time, chain, block_num, block_hash, payload, source, received_at)
			VALUES ($1, $2, $3, $4, $5, $6, now())`,
			b.Time, c.Chain, int64(b.Num), b.Hash, encoder.EncodeAll(b.Payload, nil), b.Source)
	}
	batch.Queue(`INSERT INTO chain_blocks (chain, block_num, block_hash, parent_hash, block_time, status, tx_count, ingested_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (chain, block_num) DO UPDATE SET block_hash = EXCLUDED.block_hash, parent_hash = EXCLUDED.parent_hash,
			block_time = EXCLUDED.block_time, status = EXCLUDED.status, tx_count = EXCLUDED.tx_count, ingested_at = EXCLUDED.ingested_at,
			parsed_version = NULL, parsed_at = NULL, hit_txs = NULL, events = NULL,
            balance_version = NULL, balance_parsed_at = NULL`,
		c.Chain, int64(b.Num), hash, parent, at, b.Status, b.TxCount)
	if f := c.Forward; f != nil {
		batch.Queue(`INSERT INTO stream_cursors (chain, stream, anchor, position, head, last_block_time, updated_at)
			VALUES ($1, 'forward', $2, $3, $4, $5, now())
			ON CONFLICT (chain, stream) DO UPDATE SET anchor = EXCLUDED.anchor, position = EXCLUDED.position, head = EXCLUDED.head,
				last_block_time = COALESCE(EXCLUDED.last_block_time, stream_cursors.last_block_time),
				last_error_kind = NULL, last_error = NULL, last_error_at = NULL, updated_at = EXCLUDED.updated_at`,
			c.Chain, int64(f.Anchor), int64(f.Position), int64(f.Head), at)
	}
	if g := c.NewGap; g != nil {
		batch.Queue(`INSERT INTO stream_gaps (chain, from_pos, to_pos, reason, created_at, updated_at)
			VALUES ($1, $2, $3, $4, now(), now())`, c.Chain, int64(g.From), int64(g.To), g.Reason)
	}
	fill := -1
	if c.FillGap != 0 {
		// The filled block is the gap's top; filling its bottom closes it.
		fill = batch.Len()
		batch.Queue(`UPDATE stream_gaps SET
				status = CASE WHEN from_pos = $3 THEN 'filled' ELSE 'filling' END,
				to_pos = CASE WHEN from_pos = $3 THEN to_pos ELSE $3 - 1 END,
				updated_at = now()
			WHERE id = $1 AND chain = $2 AND to_pos = $3 AND status <> 'filled'`, c.FillGap, c.Chain, int64(b.Num))
	}
	return fill
}

// partitionLock serialises partition DDL across ingest processes.
const partitionLock = 7_100_002

// ensurePartition creates the daily partition of table holding t.
func (s *Store) ensurePartition(ctx context.Context, table string, t time.Time) error {
	day := t.UTC().Truncate(24 * time.Hour)
	name := table + "_" + day.Format("20060102")
	if _, ok := s.days.Load(table + day.Format("20060102")); ok {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: partition %s: %w", name, err)
	}
	defer tx.Rollback(ctx)
	sqls := []string{
		"SELECT pg_advisory_xact_lock(" + fmt.Sprint(partitionLock) + ")",
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')",
			name, table, day.Format(time.RFC3339), day.Add(24*time.Hour).Format(time.RFC3339)),
	}
	if table == "raw_blocks" {
		// The payload is already zstd-compressed, so TOAST compression is skipped.
		sqls = append(sqls, "ALTER TABLE "+name+" ALTER COLUMN payload SET STORAGE EXTERNAL")
	}
	for _, sql := range sqls {
		if _, err := tx.Exec(ctx, sql); err != nil {
			return fmt.Errorf("store: partition %s: %w", name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: partition %s: %w", name, err)
	}
	s.days.Store(table+day.Format("20060102"), true)
	return nil
}

// RawBlock returns the decompressed payload of a stored block.
func (s *Store) RawBlock(ctx context.Context, chain string, num uint64) ([]byte, error) {
	var z []byte
	err := s.pool.QueryRow(ctx, "SELECT payload FROM raw_blocks WHERE chain = $1 AND block_num = $2", chain, int64(num)).Scan(&z)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: raw block %s %d: %w", chain, num, err)
	}
	out, err := decoder.DecodeAll(z, nil)
	if err != nil {
		return nil, fmt.Errorf("store: raw block %s %d: %w", chain, num, err)
	}
	return out, nil
}

// BlockHash returns the hash recorded for a non-skipped block.
func (s *Store) BlockHash(ctx context.Context, chain string, num uint64) ([]byte, error) {
	var h []byte
	err := s.pool.QueryRow(ctx, "SELECT block_hash FROM chain_blocks WHERE chain = $1 AND block_num = $2 AND block_hash IS NOT NULL",
		chain, int64(num)).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: block hash %s %d: %w", chain, num, err)
	}
	return h, nil
}

// ForwardCursor returns the forward cursor of chain.
func (s *Store) ForwardCursor(ctx context.Context, chain string) (Cursor, error) {
	var a, p, h int64
	err := s.pool.QueryRow(ctx, "SELECT anchor, position, head FROM stream_cursors WHERE chain = $1 AND stream = 'forward'", chain).Scan(&a, &p, &h)
	if errors.Is(err, pgx.ErrNoRows) {
		return Cursor{}, ErrNotFound
	}
	if err != nil {
		return Cursor{}, fmt.Errorf("store: cursor %s: %w", chain, err)
	}
	return Cursor{Anchor: uint64(a), Position: uint64(p), Head: uint64(h)}, nil
}

// OpenGaps returns unfilled gaps of chain, highest range first.
func (s *Store) OpenGaps(ctx context.Context, chain string) ([]Gap, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, from_pos, to_pos, reason FROM stream_gaps
		WHERE chain = $1 AND status <> 'filled' ORDER BY to_pos DESC`, chain)
	if err != nil {
		return nil, fmt.Errorf("store: gaps %s: %w", chain, err)
	}
	gaps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Gap, error) {
		var g Gap
		var from, to int64
		err := r.Scan(&g.ID, &from, &to, &g.Reason)
		g.From, g.To = uint64(from), uint64(to)
		return g, err
	})
	if err != nil {
		return nil, fmt.Errorf("store: gaps %s: %w", chain, err)
	}
	return gaps, nil
}

// BlockStatus returns the ledger status of a block.
func (s *Store) BlockStatus(ctx context.Context, chain string, num uint64) (string, error) {
	var st string
	err := s.pool.QueryRow(ctx, "SELECT status FROM chain_blocks WHERE chain = $1 AND block_num = $2", chain, int64(num)).Scan(&st)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: block status %s %d: %w", chain, num, err)
	}
	return st, nil
}

// PromoteConfirmed marks pending blocks at or below upTo as confirmed, with
// the trades and liquidity events already parsed from them.
func (s *Store) PromoteConfirmed(ctx context.Context, chain string, upTo uint64) error {
	if _, err := s.pool.Exec(ctx, `WITH promoted AS (
			UPDATE chain_blocks SET status = 'confirmed'
			WHERE chain = $1 AND status = 'pending' AND block_num <= $2 RETURNING block_num
		), trades_done AS (
			UPDATE trades SET confirmed = true
			WHERE chain = $1 AND block_num IN (SELECT block_num FROM promoted) AND NOT confirmed
		)
		UPDATE liquidity_events SET confirmed = true
		WHERE chain = $1 AND block_num IN (SELECT block_num FROM promoted) AND NOT confirmed`, chain, int64(upTo)); err != nil {
		return fmt.Errorf("store: promote %s %d: %w", chain, upTo, err)
	}
	return nil
}

// Rollback deletes every block above ancestor with its facts and moves the
// forward cursor back to it, in one transaction (EVM reorg).
func (s *Store) Rollback(ctx context.Context, chain string, ancestor uint64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: rollback %s %d: %w", chain, ancestor, err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7103,hashtext($1))`, chain); err != nil {
		return fmt.Errorf("store: rollback balance lock: %w", err)
	}
	for _, sql := range []string{
		"DELETE FROM trades WHERE chain = $1 AND block_num > $2",
		"DELETE FROM liquidity_events WHERE chain = $1 AND block_num > $2",
		"DELETE FROM decode_failures WHERE chain = $1 AND block_num > $2",
		"DELETE FROM balance_deltas WHERE chain = $1 AND block_num > $2",
		"DELETE FROM raw_blocks WHERE chain = $1 AND block_num > $2",
		"DELETE FROM chain_blocks WHERE chain = $1 AND block_num > $2",
		"UPDATE stream_cursors SET position = $2, updated_at = now() WHERE chain = $1 AND stream = 'forward' AND position > $2",
	} {
		if _, err := tx.Exec(ctx, sql, chain, int64(ancestor)); err != nil {
			return fmt.Errorf("store: rollback %s %d: %w", chain, ancestor, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: rollback %s %d: %w", chain, ancestor, err)
	}
	return nil
}

// LockChain takes the chain's ingest lock on a dedicated connection; unlock
// releases it. It fails with ErrLocked when another session holds it.
func (s *Store) LockChain(ctx context.Context, chain string) (unlock func(), err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: lock %s: %w", chain, err)
	}
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1, hashtext($2))", chainLockSpace, chain).Scan(&ok); err != nil {
		conn.Release()
		return nil, fmt.Errorf("store: lock %s: %w", chain, err)
	}
	if !ok {
		conn.Release()
		return nil, ErrLocked
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// A failed unlock leaves the session lock held, so drop the connection.
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1, hashtext($2))", chainLockSpace, chain); err != nil {
			conn.Conn().Close(ctx)
		}
		conn.Release()
	}, nil
}

// chainLockSpace is the first key of the per-chain ingest advisory lock.
const chainLockSpace = 7101

// Prune drops raw_blocks partitions that end at or before rawBefore, trades
// and liquidity_events partitions that end at or before ledgerBefore, and
// deletes ledger rows older than ledgerBefore. A zero cutoff keeps that
// table (raw blocks are then deleted by hand with PruneRaw).
func (s *Store) Prune(ctx context.Context, rawBefore, ledgerBefore time.Time) error {
	for table, before := range map[string]time.Time{"raw_blocks": rawBefore, "trades": ledgerBefore, "liquidity_events": ledgerBefore} {
		if before.IsZero() {
			continue // kept until pruned by hand
		}
		if err := s.dropPartitions(ctx, table, before); err != nil {
			return err
		}
	}
	if err := s.pruneBalanceLedger(ctx, rawBefore, ledgerBefore); err != nil {
		return err
	}
	// Skipped slots carry no time; drop those below the oldest dated block.
	if _, err := s.pool.Exec(ctx, `DELETE FROM chain_blocks c WHERE status = 'skipped' AND block_num <
		(SELECT min(block_num) FROM chain_blocks d WHERE d.chain = c.chain AND d.block_time IS NOT NULL)`); err != nil {
		return fmt.Errorf("store: prune skipped: %w", err)
	}
	return nil
}

// dropPartitions drops table's daily partitions that end at or before before.
func (s *Store) dropPartitions(ctx context.Context, table string, before time.Time) error {
	rows, err := s.pool.Query(ctx, `SELECT c.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid WHERE i.inhparent = $1::regclass`, table)
	if err != nil {
		return fmt.Errorf("store: prune: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("store: prune: %w", err)
	}
	for _, name := range names {
		day, err := time.Parse(table+"_20060102", name)
		if err != nil || day.Add(24*time.Hour).After(before) {
			continue
		}
		if _, err := s.pool.Exec(ctx, "DROP TABLE IF EXISTS "+name); err != nil {
			return fmt.Errorf("store: prune %s: %w", name, err)
		}
		s.days.Delete(table + day.Format("20060102"))
	}
	return nil
}

// PruneRaw deletes raw blocks older than before by hand: whole daily
// partitions for every chain, or one chain's rows when chain is set.
func (s *Store) PruneRaw(ctx context.Context, before time.Time, chain string) error {
	if chain == "" {
		return s.dropPartitions(ctx, "raw_blocks", before)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM raw_blocks WHERE chain = $1 AND block_time < $2`, chain, before); err != nil {
		return fmt.Errorf("store: prune raw %s: %w", chain, err)
	}
	return nil
}

// EnsureWindowGap opens a startup gap [from, oldest-1] when the backfill
// window starts below the oldest block chain knows of (in the ledger or in
// any gap); a chain without blocks is left to its forward follower.
func (s *Store) EnsureWindowGap(ctx context.Context, chain string, from uint64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO stream_gaps (chain, from_pos, to_pos, reason, created_at, updated_at)
		SELECT $1, $2, low - 1, 'startup', now(), now() FROM (
			SELECT least((SELECT min(block_num) FROM chain_blocks WHERE chain = $1), (SELECT min(from_pos) FROM stream_gaps WHERE chain = $1)) AS low
		) x WHERE low IS NOT NULL AND $2 < low`, chain, int64(from))
	if err != nil {
		return fmt.Errorf("store: window gap: %w", err)
	}
	return nil
}

// SetForwardError records the latest source error on the forward cursor; the
// next successful forward commit clears it.
func (s *Store) SetForwardError(ctx context.Context, chain, kind, msg string) error {
	if _, err := s.pool.Exec(ctx, `UPDATE stream_cursors SET last_error_kind = $2, last_error = $3,
		last_error_at = now(), updated_at = now() WHERE chain = $1 AND stream = 'forward'`, chain, kind, msg); err != nil {
		return fmt.Errorf("store: forward error %s: %w", chain, err)
	}
	return nil
}

// ForwardError returns the error recorded on the forward cursor, empty when
// the last forward commit succeeded.
func (s *Store) ForwardError(ctx context.Context, chain string) (kind, msg string, err error) {
	var k, m *string
	err = s.pool.QueryRow(ctx, "SELECT last_error_kind, last_error FROM stream_cursors WHERE chain = $1 AND stream = 'forward'",
		chain).Scan(&k, &m)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("store: forward error %s: %w", chain, err)
	}
	if k != nil {
		kind = *k
	}
	if m != nil {
		msg = *m
	}
	return kind, msg, nil
}

// EnsureChain adds name to the chain_id enum if missing, so a chain added in
// the configuration needs no migration. Existing tables are not rewritten.
func (s *Store) EnsureChain(ctx context.Context, name string) error {
	if !chainName.MatchString(name) {
		return fmt.Errorf("store: invalid chain name %q", name)
	}
	// ADD VALUE cannot take a bind parameter; the name is validated above.
	if _, err := s.pool.Exec(ctx, "ALTER TYPE chain_id ADD VALUE IF NOT EXISTS '"+name+"'"); err != nil {
		return fmt.Errorf("store: ensure chain %s: %w", name, err)
	}
	return nil
}

// chainName is the accepted form of configured chain names.
var chainName = regexp.MustCompile(`^[a-z][a-z0-9_]{1,30}$`)

// TokenLookup returns a cached lookup result.
func (s *Store) TokenLookup(ctx context.Context, chain, kind string, address []byte) ([]byte, bool, error) {
	var res []byte
	err := s.pool.QueryRow(ctx, "SELECT result FROM token_lookups WHERE chain = $1 AND address = $2 AND kind = $3",
		chain, address, kind).Scan(&res)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: token lookup %s %s: %w", chain, kind, err)
	}
	return res, true, nil
}

// SaveTokenLookup caches a lookup result (JSON).
func (s *Store) SaveTokenLookup(ctx context.Context, chain, kind string, address, result []byte) error {
	if _, err := s.pool.Exec(ctx, `INSERT INTO token_lookups (chain, address, kind, result, checked_at)
		VALUES ($1, $2, $3, $4, now()) ON CONFLICT (chain, address, kind) DO UPDATE SET result = EXCLUDED.result, checked_at = now()`,
		chain, address, kind, result); err != nil {
		return fmt.Errorf("store: save token lookup %s %s: %w", chain, kind, err)
	}
	return nil
}
