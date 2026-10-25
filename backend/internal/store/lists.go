package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ImportRow preserves one validated upload row, including invalid raw input.
type ImportRow struct {
	Line    int64    `json:"line"`
	Chain   *string  `json:"chain"`
	Address *string  `json:"address"`
	Label   *string  `json:"label"`
	Tags    []string `json:"tags"`
	Twitter *string  `json:"twitter"`
	Source  *string  `json:"source"`
	Status  string   `json:"status"`
	Error   *string  `json:"error"`
}

// ImportTotals counts all upload rows before the preview is truncated.
type ImportTotals struct {
	Rows      int64 `json:"rows"`
	Valid     int64 `json:"valid"`
	Invalid   int64 `json:"invalid"`
	Duplicate int64 `json:"duplicate"`
	Existing  int64 `json:"existing"`
}

// ListImport is a durable preview. Rows are retained in original line order.
type ListImport struct {
	ID, FileName         string
	CreatedAt, ExpiresAt time.Time
	Applied              bool
	AppliedVersion       *int64
	RowsArchived         bool
	Totals               ImportTotals
	Rows                 []ImportRow
}

// ActiveListWalletIDs returns active snapshot identities, including exclusions.
func (s *Store) ActiveListWalletIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.chain::text || ':' || addr(m.chain,m.address)
        FROM sm_list_members m JOIN sm_lists l USING(version) WHERE l.status='active'`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// importKey maps the mock-compatible opaque report ID to its UUID storage key.
func importKey(id string) (pgtype.UUID, error) {
	var key pgtype.UUID
	raw, err := hex.DecodeString(strings.TrimPrefix(id, "imp_"))
	if !strings.HasPrefix(id, "imp_") || err != nil || len(raw) != 16 {
		return key, ErrNotFound
	}
	copy(key.Bytes[:], raw)
	key.Valid = true
	return key, nil
}

// SaveListImport atomically persists the report header and all validated rows.
func (s *Store) SaveListImport(ctx context.Context, v ListImport) error {
	key, err := importKey(v.ID)
	if err != nil {
		return err
	}
	totals, err := json.Marshal(v.Totals)
	if err != nil {
		return err
	}
	rows, err := json.Marshal(v.Rows)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var batch pgx.Batch
	batch.Queue(`INSERT INTO sm_imports(id,file_name,created_at,expires_at,totals) VALUES($1,$2,$3,$4,$5)`, key, v.FileName, v.CreatedAt, v.ExpiresAt, totals)
	batch.Queue(`INSERT INTO sm_import_rows(import_id,line,chain,address,label,tags,twitter,source,status,error)
        SELECT $1,line,chain,address,label,tags,twitter,source,status,error
        FROM jsonb_to_recordset($2::jsonb) AS r(line integer,chain text,address text,label text,tags text[],twitter text,source text,status text,error text)`, key, rows)
	if err := tx.SendBatch(ctx, &batch).Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListImportReport reads an upload, hiding expired reports that were not applied.
func (s *Store) ListImportReport(ctx context.Context, id string, now time.Time) (ListImport, error) {
	key, err := importKey(id)
	if err != nil {
		return ListImport{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ListImport{}, err
	}
	defer tx.Rollback(ctx)
	var v ListImport
	var totals []byte
	err = tx.QueryRow(ctx, `SELECT 'imp_'||replace(id::text,'-',''),file_name,created_at,expires_at,applied,applied_list_version,rows_archived,totals
        FROM sm_imports WHERE id=$1 AND (applied OR expires_at>$2)`, key, now).
		Scan(&v.ID, &v.FileName, &v.CreatedAt, &v.ExpiresAt, &v.Applied, &v.AppliedVersion, &v.RowsArchived, &totals)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(totals, &v.Totals); err != nil {
		return v, err
	}
	rows, err := tx.Query(ctx, `SELECT line,chain,address,label,tags,twitter,source,status,error FROM sm_import_rows WHERE import_id=$1 ORDER BY line`, key)
	if err != nil {
		return v, err
	}
	v.Rows, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ImportRow, error) {
		var row ImportRow
		err := r.Scan(&row.Line, &row.Chain, &row.Address, &row.Label, &row.Tags, &row.Twitter, &row.Source, &row.Status, &row.Error)
		return row, err
	})
	if err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}

// ErrImportClosed reports an expired or already-applied preview.
var ErrImportClosed = errors.New("store: import already applied or expired")

// ListVersion is a version's build state, with the active head at query time.
type ListVersion struct {
	Version, Parent, Active              int64
	Status, BuildStatus                  string
	ErrorCode                            *string
	Added, Merged, Removed, TotalMembers int64
	CreatedAt                            time.Time
	ActivatedAt                          *time.Time
}

// listWriteLock serializes API draft changes without locking compute's snapshots.
const listWriteLock = 7_100_013

// ApplyListImport atomically accepts one upload and queues a compute-owned build.
func (s *Store) ApplyListImport(ctx context.Context, id string, now time.Time) (ListVersion, error) {
	key, err := importKey(id)
	if err != nil {
		return ListVersion{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ListVersion{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", listWriteLock); err != nil {
		return ListVersion{}, err
	}
	var applied bool
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT applied,expires_at FROM sm_imports WHERE id=$1 FOR UPDATE`, key).Scan(&applied, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return ListVersion{}, ErrNotFound
	}
	if err != nil {
		return ListVersion{}, err
	}
	if applied || !now.Before(expires) {
		return ListVersion{}, ErrImportClosed
	}
	var v ListVersion
	v.Status, v.BuildStatus, v.CreatedAt = "building", "queued", now
	err = tx.QueryRow(ctx, `SELECT (SELECT max(version) FROM sm_lists),(SELECT version FROM sm_lists WHERE status='active'),(SELECT count(*) FROM sm_wallets)`).Scan(&v.Parent, &v.Active, &v.TotalMembers)
	if err != nil {
		return v, err
	}
	v.Version = v.Parent + 1
	err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE w.address IS NULL),count(*) FILTER(WHERE w.address IS NOT NULL)
        FROM sm_import_rows r LEFT JOIN sm_wallets w ON w.chain=r.chain::chain_id AND w.address=from_addr(r.chain::chain_id,r.address)
        WHERE r.import_id=$1 AND r.status IN ('valid','existing')`, key).Scan(&v.Added, &v.Merged)
	if err != nil {
		return v, err
	}
	v.TotalMembers += v.Added
	var batch pgx.Batch
	batch.Queue(`INSERT INTO sm_lists(version,parent_version,status,build_status,added,merged,total_members,created_at)
        VALUES($1,$2,'building','queued',$3,$4,$5,$6)`, v.Version, v.Parent, v.Added, v.Merged, v.TotalMembers, now)
	batch.Queue(`INSERT INTO sm_list_changes(version,op,chain,address,label,tags,twitter,source)
        SELECT $1,CASE WHEN status='existing' THEN 'merge' ELSE 'add' END,chain::chain_id,from_addr(chain::chain_id,address),label,tags,twitter,'import'
        FROM sm_import_rows WHERE import_id=$2 AND status IN ('valid','existing') ORDER BY line`, v.Version, key)
	batch.Queue(`INSERT INTO sm_wallets(chain,address,label,tags,twitter,sources,first_added_at,updated_at)
        SELECT chain::chain_id,from_addr(chain::chain_id,address),label,ARRAY(SELECT t FROM unnest(tags) t ORDER BY t),twitter,ARRAY['import'],$2,$2
        FROM sm_import_rows WHERE import_id=$1 AND status IN ('valid','existing')
        ON CONFLICT(chain,address) DO UPDATE SET
        label=coalesce(sm_wallets.label,excluded.label),twitter=coalesce(sm_wallets.twitter,excluded.twitter),
        tags=ARRAY(SELECT DISTINCT t FROM unnest(sm_wallets.tags || excluded.tags) t ORDER BY t),
        sources=CASE WHEN 'import'=ANY(sm_wallets.sources) THEN sm_wallets.sources ELSE array_append(sm_wallets.sources,'import') END,
        updated_at=excluded.updated_at`, key, now)
	batch.Queue(`UPDATE sm_imports SET applied=true,applied_list_version=$2,applied_at=$3 WHERE id=$1`, key, v.Version, now)
	batch.Queue(`INSERT INTO jobs(kind,payload,status,created_at,updated_at) VALUES('list_build',jsonb_build_object('version',$1::bigint),'queued',$2,$2)`, v.Version, now)
	if err = tx.SendBatch(ctx, &batch).Close(); err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}

// ListMember is an immutable, version-specific wallet record.
type ListMember struct {
	Chain    string
	Address  []byte
	Label    *string
	Tags     []string
	Twitter  *string
	Sources  []string
	AddedAt  time.Time
	Excluded bool
}

// ListChange is one ordered operation applied to a parent's snapshot.
type ListChange struct {
	Op, Chain               string
	Address                 []byte
	Label                   *string
	Tags                    []string
	Twitter, Source, Reason *string
}

// ListJob is a fenced lease: Attempts changes on each successful claim.
type ListJob struct {
	TotalMembers        int64
	ID, Version, Parent int64
	Attempts            int
	LeaseUntil          time.Time
	CreatedAt           time.Time
}

// ErrJobLeaseLost prevents a stale worker from publishing after a new claim.
var ErrJobLeaseLost = errors.New("store: list job lease lost")

// ActiveListVersion returns the only published list head.
func (s *Store) ActiveListVersion(ctx context.Context) (int64, error) {
	var version int64
	err := s.pool.QueryRow(ctx, `SELECT version FROM sm_lists WHERE status='active'`).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return version, err
}

// ListBuild returns the persisted state and current active version in one query.
func (s *Store) ListBuild(ctx context.Context, version int64) (ListVersion, error) {
	var v ListVersion
	err := s.pool.QueryRow(ctx, `SELECT l.version,coalesce(l.parent_version,l.version),a.version,l.status,l.build_status,l.error_code,
        l.added,l.merged,l.removed,l.total_members,l.created_at,l.activated_at
        FROM sm_lists l CROSS JOIN sm_lists a WHERE l.version=$1 AND a.status='active'`, version).
		Scan(&v.Version, &v.Parent, &v.Active, &v.Status, &v.BuildStatus, &v.ErrorCode, &v.Added, &v.Merged, &v.Removed, &v.TotalMembers, &v.CreatedAt, &v.ActivatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}

// ClaimListBuild leases one eligible job. Only the child of active can run.
func (s *Store) ClaimListBuild(ctx context.Context, now time.Time) (ListJob, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ListJob{}, false, err
	}
	defer tx.Rollback(ctx)
	var j ListJob
	err = tx.QueryRow(ctx, `SELECT j.id,l.version,l.parent_version,j.attempts,l.created_at,l.total_members FROM jobs j
        JOIN sm_lists l ON l.version=(j.payload->>'version')::bigint
        JOIN sm_lists a ON a.version=l.parent_version AND a.status='active'
        WHERE j.kind='list_build' AND ((j.status='queued' AND l.build_status='queued')
        OR (j.status='running' AND l.build_status='running' AND j.lease_until<=$1))
        ORDER BY j.id FOR UPDATE OF j SKIP LOCKED LIMIT 1`, now).Scan(&j.ID, &j.Version, &j.Parent, &j.Attempts, &j.CreatedAt, &j.TotalMembers)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, false, nil
	}
	if err != nil {
		return j, false, err
	}
	j.Attempts++
	j.LeaseUntil = now.Add(time.Minute)
	var batch pgx.Batch
	batch.Queue(`UPDATE jobs SET status='running',attempts=$2,lease_until=$3,updated_at=$4 WHERE id=$1`, j.ID, j.Attempts, j.LeaseUntil, now)
	batch.Queue(`UPDATE sm_lists SET build_status='running' WHERE version=$1`, j.Version)
	if err = tx.SendBatch(ctx, &batch).Close(); err != nil {
		return j, false, err
	}
	err = tx.Commit(ctx)
	return j, err == nil, err
}

// ListBuildInput reads the immutable parent and this version's ordered changes.
func (s *Store) ListBuildInput(ctx context.Context, j ListJob) ([]ListMember, []ListChange, error) {
	rows, err := s.pool.Query(ctx, `SELECT chain::text,address,label,tags,twitter,sources,added_at,excluded FROM sm_list_members WHERE version=$1 ORDER BY chain,address`, j.Parent)
	if err != nil {
		return nil, nil, err
	}
	members, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ListMember, error) {
		var m ListMember
		err := r.Scan(&m.Chain, &m.Address, &m.Label, &m.Tags, &m.Twitter, &m.Sources, &m.AddedAt, &m.Excluded)
		return m, err
	})
	if err != nil {
		return nil, nil, err
	}
	rows, err = s.pool.Query(ctx, `SELECT op,chain::text,address,label,tags,twitter,source,reason FROM sm_list_changes WHERE version=$1 ORDER BY id`, j.Version)
	if err != nil {
		return nil, nil, err
	}
	changes, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ListChange, error) {
		var c ListChange
		err := r.Scan(&c.Op, &c.Chain, &c.Address, &c.Label, &c.Tags, &c.Twitter, &c.Source, &c.Reason)
		return c, err
	})
	return members, changes, err
}

// ActivateListBuild publishes the snapshot, switches active, and finishes the
// fenced job in one transaction. H4's projection switch belongs in onActivate.
func (s *Store) ActivateListBuild(ctx context.Context, j ListJob, members []ListMember, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owned bool
	err = tx.QueryRow(ctx, `SELECT status='running' AND attempts=$2 AND lease_until>$3 FROM jobs WHERE id=$1 FOR UPDATE`, j.ID, j.Attempts, now).Scan(&owned)
	if err != nil {
		return err
	}
	if !owned {
		return ErrJobLeaseLost
	}
	var active int64
	if err = tx.QueryRow(ctx, `SELECT version FROM sm_lists WHERE status='active' FOR UPDATE`).Scan(&active); err != nil {
		return err
	}
	if active != j.Parent {
		return ErrJobLeaseLost
	}
	var prepared bool
	if err = tx.QueryRow(ctx, `SELECT prepared_at IS NOT NULL FROM sm_lists WHERE version=$1`, j.Version).Scan(&prepared); err != nil {
		return err
	}
	if !prepared {
		if err = copyListMembers(ctx, tx, j.Version, members); err != nil {
			return err
		}
	}
	var batch pgx.Batch
	batch.Queue(`UPDATE sm_lists SET status='retired' WHERE version=$1`, j.Parent)
	batch.Queue(`UPDATE sm_lists SET status='active',build_status='activated',activated_at=$2,error_code=NULL,total_members=$3 WHERE version=$1`, j.Version, now, len(members))
	batch.Queue(`UPDATE jobs SET status='done',lease_until=NULL,updated_at=$2 WHERE id=$1`, j.ID, now)
	if err = tx.SendBatch(ctx, &batch).Close(); err != nil {
		return err
	}
	if err = onActivate(ctx, tx, j.Version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// QueueListMutation accepts one remove/exclude/restore against the latest draft.
// Management records change here; versioned member snapshots remain compute-owned.
func (s *Store) QueueListMutation(ctx context.Context, op, chain, address, reason string, now time.Time) (ListVersion, error) {
	if op != "remove" && op != "exclude" && op != "restore" {
		return ListVersion{}, errors.New("store: unsupported list operation")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ListVersion{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", listWriteLock); err != nil {
		return ListVersion{}, err
	}
	v := ListVersion{Status: "building", BuildStatus: "queued", CreatedAt: now}
	err = tx.QueryRow(ctx, `SELECT (SELECT max(version) FROM sm_lists),(SELECT version FROM sm_lists WHERE status='active'),(SELECT count(*) FROM sm_wallets)`).Scan(&v.Parent, &v.Active, &v.TotalMembers)
	if err != nil {
		return v, err
	}
	v.Version = v.Parent + 1
	var sources []string
	err = tx.QueryRow(ctx, `SELECT sources FROM sm_wallets WHERE chain=$1 AND address=from_addr($1,$2)`, chain, address).Scan(&sources)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return v, err
	}
	imported := false
	for _, source := range sources {
		if source == "import" {
			imported = true
		}
	}
	if op == "remove" {
		if !imported {
			return v, ErrNotFound
		}
		if len(sources) == 1 {
			v.Removed = 1
			v.TotalMembers--
		} else {
			v.Merged = 1
		}
	} else if len(sources) > 0 {
		v.Merged = 1
	}
	if op == "restore" {
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sm_exclusions WHERE chain=$1 AND address=from_addr($1,$2))`, chain, address).Scan(&exists)
		if err != nil {
			return v, err
		}
		if !exists {
			return v, ErrNotFound
		}
	}
	var batch pgx.Batch
	batch.Queue(`INSERT INTO sm_lists(version,parent_version,status,build_status,merged,removed,total_members,created_at)
        VALUES($1,$2,'building','queued',$3,$4,$5,$6)`, v.Version, v.Parent, v.Merged, v.Removed, v.TotalMembers, now)
	batch.Queue(`INSERT INTO sm_list_changes(version,op,chain,address,source,reason) VALUES($1,$2,$3,from_addr($3,$4),'import',nullif($5,''))`, v.Version, op, chain, address, reason)
	switch op {
	case "remove":
		batch.Queue(`UPDATE sm_wallets SET sources=array_remove(sources,'import'),updated_at=$3 WHERE chain=$1 AND address=from_addr($1,$2)`, chain, address, now)
		batch.Queue(`DELETE FROM sm_wallets WHERE chain=$1 AND address=from_addr($1,$2) AND cardinality(sources)=0`, chain, address)
	case "exclude":
		batch.Queue(`INSERT INTO sm_exclusions(chain,address,reason,created_at,created_version) VALUES($1,from_addr($1,$2),$3,$4,$5)
            ON CONFLICT(chain,address) DO UPDATE SET reason=excluded.reason`, chain, address, reason, now, v.Version)
	case "restore":
		batch.Queue(`DELETE FROM sm_exclusions WHERE chain=$1 AND address=from_addr($1,$2)`, chain, address)
	}
	batch.Queue(`INSERT INTO jobs(kind,payload,status,created_at,updated_at) VALUES('list_build',jsonb_build_object('version',$1::bigint),'queued',$2,$2)`, v.Version, now)
	if err = tx.SendBatch(ctx, &batch).Close(); err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}

// ListExclusion is an exclusion effective at a specific immutable version.
type ListExclusion struct {
	Chain     string
	Address   []byte
	Reason    string
	CreatedAt time.Time
}

// ListExclusions replays only ancestors of version. Management intentions from
// queued/failed descendants never change active reads or a rebuild's result.
// Repeated excludes update the reason while preserving creation until restore.
func (s *Store) ListExclusions(ctx context.Context, version int64) ([]ListExclusion, error) {
	rows, err := s.pool.Query(ctx, `WITH RECURSIVE lineage AS (
        SELECT version,parent_version,created_at FROM sm_lists WHERE version=$1
        UNION ALL SELECT p.version,p.parent_version,p.created_at FROM sm_lists p JOIN lineage c ON p.version=c.parent_version
    ), events AS (
        SELECT c.id,c.version,c.chain,c.address,c.op,c.reason,l.created_at FROM sm_list_changes c JOIN lineage l USING(version) WHERE c.op IN ('exclude','restore')
    ), epochs AS (
        SELECT *,sum((op='restore')::integer) OVER(PARTITION BY chain,address ORDER BY version,id) AS epoch FROM events
    ), dated AS (
        SELECT *,first_value(created_at) OVER(PARTITION BY chain,address,epoch ORDER BY (op='exclude') DESC,version,id) AS excluded_at,
        row_number() OVER(PARTITION BY chain,address ORDER BY version DESC,id DESC) AS rn FROM epochs
    ) SELECT chain::text,address,reason,excluded_at FROM dated WHERE rn=1 AND op='exclude' ORDER BY chain::text,address`, version)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ListExclusion, error) {
		var e ListExclusion
		err := r.Scan(&e.Chain, &e.Address, &e.Reason, &e.CreatedAt)
		return e, err
	})
}

// ListJobBacklog returns queued list jobs and their oldest enqueue time.
func (s *Store) ListJobBacklog(ctx context.Context) (int64, *time.Time, error) {
	var count int64
	var oldest *time.Time
	err := s.pool.QueryRow(ctx, `SELECT count(*),min(created_at) FROM jobs WHERE kind='list_build' AND status='queued'`).Scan(&count, &oldest)
	return count, oldest, err
}

// FailListBuild records a terminal build failure only while this lease is owned.
func (s *Store) FailListBuild(ctx context.Context, j ListJob, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE jobs SET status='failed',lease_until=NULL,updated_at=$3 WHERE id=$1 AND attempts=$2 AND status='running' AND lease_until>$3`, j.ID, j.Attempts, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrJobLeaseLost
	}
	if _, err = tx.Exec(ctx, `UPDATE sm_lists SET build_status='failed',error_code='build_error' WHERE version=$1`, j.Version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RetryListBuild queues a fresh job only for failed versions, leaving all other
// states unchanged. The API never rebuilds a snapshot itself.
func (s *Store) RetryListBuild(ctx context.Context, version int64, now time.Time) (ListVersion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ListVersion{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", listWriteLock); err != nil {
		return ListVersion{}, err
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT build_status FROM sm_lists WHERE version=$1 FOR UPDATE`, version).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ListVersion{}, ErrNotFound
	}
	if err != nil {
		return ListVersion{}, err
	}
	if status == "failed" {
		var batch pgx.Batch
		batch.Queue(`UPDATE sm_lists SET build_status='queued',error_code=NULL WHERE version=$1`, version)
		batch.Queue(`INSERT INTO jobs(kind,payload,status,created_at,updated_at) VALUES('list_build',jsonb_build_object('version',$1::bigint),'queued',$2,$2)`, version, now)
		if err = tx.SendBatch(ctx, &batch).Close(); err != nil {
			return ListVersion{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ListVersion{}, err
	}
	return s.ListBuild(ctx, version)
}

// ListMemberFilter describes literal filters and a (chain,address) keyset.
type ListMemberFilter struct {
	Chain, Source, Tag, Q string
	AfterChain            string
	AfterAddress          []byte
	Limit                 int
}

// ListMemberPage counts the whole filtered snapshot, even on later pages.
type ListMemberPage struct {
	Total   int64
	Items   []ListMember
	HasMore bool
}

// ListMembers reads one immutable version, ordered by textual chain then raw key.
func (s *Store) ListMembers(ctx context.Context, version int64, f ListMemberFilter) (ListMemberPage, error) {
	const where = `FROM sm_list_members WHERE version=$1 AND ($2='' OR chain::text=$2)
        AND ($3='all' OR $3=ANY(sources)) AND ($4='' OR $4=ANY(tags))
        AND ($5='' OR lower(addr(chain,address)) LIKE $5||'%' OR lower(coalesce(label,'')) LIKE '%'||$5||'%')`
	q := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(f.Q))
	args := []any{version, f.Chain, f.Source, f.Tag, q}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ListMemberPage{}, err
	}
	defer tx.Rollback(ctx)
	var page ListMemberPage
	if err = tx.QueryRow(ctx, `SELECT count(*) `+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	args = append(args, f.AfterChain, f.AfterAddress, f.Limit+1)
	rows, err := tx.Query(ctx, `SELECT chain::text,address,label,tags,twitter,sources,added_at,excluded `+where+`
        AND ($6='' OR (chain::text COLLATE "C",address)>($6::text COLLATE "C",$7::bytea))
        ORDER BY chain::text COLLATE "C",address LIMIT $8`, args...)
	if err != nil {
		return page, err
	}
	page.Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ListMember, error) {
		var m ListMember
		err := r.Scan(&m.Chain, &m.Address, &m.Label, &m.Tags, &m.Twitter, &m.Sources, &m.AddedAt, &m.Excluded)
		return m, err
	})
	if err != nil {
		return page, err
	}
	page.HasMore = len(page.Items) > f.Limit
	if page.HasMore {
		page.Items = page.Items[:f.Limit]
	}
	return page, tx.Commit(ctx)
}

// SearchListWallets finds active wallet addresses or literal label substrings.
// The HTTP search keeps the existing independent token-result limit.
func (s *Store) SearchListWallets(ctx context.Context, q, chain, family string, address []byte) ([]ListMember, error) {
	pattern := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(q))
	rows, err := s.pool.Query(ctx, `SELECT m.chain::text,m.address,m.label,m.tags,m.twitter,m.sources,m.added_at,m.excluded
        FROM sm_list_members m JOIN sm_lists l USING(version) WHERE l.status='active' AND ($2='' OR m.chain::text=$2)
        AND (($3='solana' AND m.chain='solana' AND m.address=$4::bytea)
            OR ($3='evm' AND m.chain<>'solana' AND m.address=$4::bytea)
            OR lower(coalesce(m.label,'')) LIKE '%'||$1||'%')
        ORDER BY m.chain::text COLLATE "C",m.address LIMIT 5`, pattern, chain, family, address)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ListMember, error) {
		var m ListMember
		err := r.Scan(&m.Chain, &m.Address, &m.Label, &m.Tags, &m.Twitter, &m.Sources, &m.AddedAt, &m.Excluded)
		return m, err
	})
}

// ArchiveListImports removes applied row details after seven days, atomically
// marking their retained reports as archived. Unapplied previews are untouched.
func (s *Store) ArchiveListImports(ctx context.Context, now time.Time) error {
	_, err := s.pool.Exec(ctx, `WITH archived AS (
        UPDATE sm_imports SET rows_archived=true WHERE applied AND NOT rows_archived
            AND applied_at<=$1::timestamptz-interval '7 days' RETURNING id
    ) DELETE FROM sm_import_rows r USING archived a WHERE r.import_id=a.id`, now)
	return err
}
