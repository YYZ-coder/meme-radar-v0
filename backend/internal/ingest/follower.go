package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/meme-radar/radar/internal/metrics"
	"log/slog"
	"sync"
	"time"

	"github.com/meme-radar/radar/internal/store"
)

// Config configures a Follower.
type Config struct {
	Chain  string
	Source Source
	// BackfillSource serves gap filling when set (an endpoint that keeps
	// older blocks); otherwise Source does.
	BackfillSource Source
	SourceName     string // recorded in raw_blocks.source
	Store          *store.Store
	// FollowHead fetches the newest block first and leaves the blocks it
	// jumped over as a rate_limited gap (free-tier Solana, D144). Otherwise
	// blocks are followed in order.
	FollowHead bool
	// Confirmations is the depth at which a block becomes confirmed; zero
	// confirms blocks immediately.
	Confirmations uint64
	// ReorgDepth bounds the common-ancestor search; zero disables parent
	// hash checks.
	ReorgDepth uint64
	// Backfill is how many blocks behind the head are filled from gaps
	// while the forward stream is idle; zero disables filling.
	Backfill uint64
	// Prefetch is how many blocks in-order following and gap filling fetch
	// concurrently before committing them in order; below 2 fetches one at a
	// time. It hides per-request latency of slow endpoints.
	Prefetch int
	// MaxLag, when set, is how far behind the head in-order following may
	// fall before it jumps to the head and leaves the rest as a gap for the
	// backfill role; zero keeps the backfill window as the threshold.
	MaxLag uint64
	// SkipFill leaves gaps to a separate backfill role (GapsOnly); the
	// window still bounds jumps and the startup gap.
	SkipFill bool
	// GapsOnly makes a backfill role: no forward following; it opens a gap
	// for the part of the window below the oldest known block and fills
	// gaps newest first.
	GapsOnly bool
	// PauseLag makes a GapsOnly follower wait while the forward follower is
	// more than this many blocks behind the head (forward first); zero
	// never waits.
	PauseLag uint64
	// RawRetention and LedgerRetention drive the hourly prune of the
	// forward follower; zero keeps rows (raw blocks until pruned by hand).
	RawRetention, LedgerRetention time.Duration
	// PollInterval is the minimum time between head requests.
	PollInterval time.Duration
	Log          *slog.Logger
}

// Follower follows one chain. It is not safe for concurrent use; run one per
// chain under store.LockChain.
type Follower struct {
	cfg     Config
	loaded  bool         // cursor read from the store
	have    bool         // cur is valid
	started bool         // a forward block was committed since start
	cur     store.Cursor // forward cursor as committed
	head    uint64
	headAt  time.Time
	backoff time.Duration // last backoff after consecutive failures
	// promotedAt is the head of the last PromoteConfirmed call.
	promotedAt uint64
}

// NewFollower returns a follower for cfg.
func NewFollower(cfg Config) *Follower { return &Follower{cfg: cfg} }

// Step does one unit of work (one block or one head poll) and returns how
// long to wait before the next step. A failure is recorded on the cursor and
// returned with its retry delay: the server's Retry-After for rate limits,
// else exponential backoff from 1s to 30s, reset by the next success.
func (f *Follower) Step(ctx context.Context) (time.Duration, error) {
	wait, err := f.step(ctx)
	if err == nil {
		f.backoff = 0
		return wait, nil
	}
	if ctx.Err() != nil {
		return 0, err
	}
	kind := "error"
	var ra retryAfter
	if errors.As(err, &ra) {
		kind, wait = "rate_limited", ra.RetryAfter()
	} else {
		f.backoff = min(max(2*f.backoff, time.Second), maxBackoff)
		wait = f.backoff
	}
	if f.have {
		if rerr := f.cfg.Store.SetForwardError(ctx, f.cfg.Chain, kind, err.Error()); rerr != nil {
			f.log().Error("record forward error", "chain", f.cfg.Chain, "err", rerr)
		}
	}
	return wait, err
}

const maxBackoff = 30 * time.Second

func (f *Follower) step(ctx context.Context) (time.Duration, error) {
	if f.cfg.GapsOnly {
		return f.backfill(ctx)
	}
	if !f.loaded {
		c, err := f.cfg.Store.ForwardCursor(ctx, f.cfg.Chain)
		switch {
		case err == nil:
			f.cur, f.have = c, true
		case !errors.Is(err, store.ErrNotFound):
			return 0, err
		}
		f.loaded = true
	}
	if f.headAt.IsZero() || time.Since(f.headAt) >= f.cfg.PollInterval {
		h, err := f.cfg.Source.Head(ctx)
		if err != nil {
			return 0, err
		}
		f.head, f.headAt = h, time.Now()
		metrics.ChainHead.WithLabelValues(f.cfg.Chain, "forward").Set(float64(h))
	}
	if !f.have || f.cur.Position < f.head {
		return f.forward(ctx)
	}
	idle := max(0, f.cfg.PollInterval-time.Since(f.headAt))
	if f.cfg.Backfill == 0 || f.cfg.SkipFill {
		return idle, nil
	}
	return f.fill(ctx, idle)
}

// backfill is a GapsOnly step: refresh the head, open a gap for the part of
// the window below the oldest known block, then fill.
func (f *Follower) backfill(ctx context.Context) (time.Duration, error) {
	if f.headAt.IsZero() || time.Since(f.headAt) >= f.cfg.PollInterval {
		h, err := f.cfg.Source.Head(ctx)
		if err != nil {
			return 0, err
		}
		f.head, f.headAt = h, time.Now()
		metrics.ChainHead.WithLabelValues(f.cfg.Chain, "backfill").Set(float64(h))
		if h > f.cfg.Backfill {
			if err := f.cfg.Store.EnsureWindowGap(ctx, f.cfg.Chain, h-f.cfg.Backfill); err != nil {
				return 0, err
			}
		}
	}
	if f.cfg.PauseLag > 0 {
		c, err := f.cfg.Store.ForwardCursor(ctx, f.cfg.Chain)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return 0, err
		}
		if err == nil && f.head > c.Position+f.cfg.PauseLag {
			return f.cfg.PollInterval, nil // forward first
		}
	}
	return f.fill(ctx, max(0, f.cfg.PollInterval-time.Since(f.headAt)))
}

// fill commits the top block of the newest gap inside the backfill window.
func (f *Follower) fill(ctx context.Context, idle time.Duration) (time.Duration, error) {
	gaps, err := f.cfg.Store.OpenGaps(ctx, f.cfg.Chain)
	if err != nil {
		return 0, err
	}
	if len(gaps) == 0 || gaps[0].To+f.cfg.Backfill < f.head {
		return idle, nil
	}
	g := gaps[0]
	nums := []uint64{g.To}
	for n := g.To; len(nums) < f.cfg.Prefetch && n > g.From && n-1+f.cfg.Backfill >= f.head; n-- {
		nums = append(nums, n-1)
	}
	var commits []store.Commit
	for i, r := range f.fetchMany(ctx, f.backfillSource(), nums) {
		if r.err != nil {
			if i > 0 {
				break // the rest is retried next step
			}
			if errors.Is(r.err, ErrNotAvailable) {
				return f.cfg.PollInterval, nil
			}
			return 0, r.err
		}
		commits = append(commits, store.Commit{Chain: f.cfg.Chain, Block: r.rec, FillGap: g.ID})
	}
	if err := f.cfg.Store.CommitBlocks(ctx, commits); err != nil {
		return 0, err
	}
	metrics.BlocksCommitted.WithLabelValues(f.cfg.Chain, "backfill").Add(float64(len(commits)))
	return 0, nil
}

// forward commits the next forward block: the head itself on a fresh start
// or in FollowHead mode, else the block after the cursor.
func (f *Follower) forward(ctx context.Context) (time.Duration, error) {
	h := f.head
	target := h
	// In-order following jumps only when it is further behind than the
	// backfill window would ever fill.
	jump := f.cfg.Backfill
	if f.cfg.MaxLag > 0 {
		jump = f.cfg.MaxLag
	}
	if f.have && !f.cfg.FollowHead && (jump == 0 || h-f.cur.Position <= jump) {
		target = f.cur.Position + 1
	}
	var gap *store.Gap
	if !f.have && f.cfg.Backfill > 0 && h > f.cfg.Backfill {
		gap = &store.Gap{From: h - f.cfg.Backfill, To: h - 1, Reason: store.GapStartup}
	}
	if f.have && target > f.cur.Position+1 {
		reason := store.GapRateLimited
		if !f.started {
			reason = store.GapStartup
		}
		gap = &store.Gap{From: f.cur.Position + 1, To: target - 1, Reason: reason}
	}
	nums := []uint64{target}
	if f.have && target == f.cur.Position+1 {
		for n := target + 1; len(nums) < f.cfg.Prefetch && n <= h; n++ {
			nums = append(nums, n)
		}
	}
	var recs []store.Block
	for i, r := range f.fetchMany(ctx, f.cfg.Source, nums) {
		if r.err != nil {
			if i > 0 {
				break // commit what precedes; the failed block is retried next step
			}
			if errors.Is(r.err, ErrNotAvailable) {
				return f.cfg.PollInterval, nil
			}
			return 0, r.err
		}
		recs = append(recs, r.rec)
	}
	return 0, f.advance(ctx, recs, gap)
}

type fetched struct {
	rec store.Block
	err error
}

// backfillSource serves gap filling.
func (f *Follower) backfillSource() Source {
	if f.cfg.BackfillSource != nil {
		return f.cfg.BackfillSource
	}
	return f.cfg.Source
}

// fetchMany fetches nums from src concurrently; results keep their order.
func (f *Follower) fetchMany(ctx context.Context, src Source, nums []uint64) []fetched {
	out := make([]fetched, len(nums))
	if len(nums) == 1 {
		out[0].rec, out[0].err = f.fetch(ctx, src, nums[0])
		return out
	}
	var wg sync.WaitGroup
	for i, n := range nums {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i].rec, out[i].err = f.fetch(ctx, src, n)
		}()
	}
	wg.Wait()
	return out
}

// advance commits consecutive forward blocks in one transaction. The first
// must extend the stored chain (else a reorg is handled); later ones must
// extend the block before them, and the batch stops at the first that does not.
func (f *Follower) advance(ctx context.Context, recs []store.Block, gap *store.Gap) error {
	if len(recs) == 0 {
		return nil
	}
	h := f.head
	if f.cfg.ReorgDepth > 0 && f.have && recs[0].Status != store.StatusSkipped && recs[0].Num == f.cur.Position+1 {
		parent, err := f.cfg.Store.BlockHash(ctx, f.cfg.Chain, f.cur.Position)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err == nil && !bytes.Equal(parent, recs[0].ParentHash) {
			return f.reorg(ctx)
		}
	}
	for i := 1; i < len(recs) && f.cfg.ReorgDepth > 0; i++ {
		prev, cur := recs[i-1], recs[i]
		if prev.Status != store.StatusSkipped && cur.Status != store.StatusSkipped && !bytes.Equal(prev.Hash, cur.ParentHash) {
			recs = recs[:i] // the source changed mid-fetch; the rest is refetched
			break
		}
	}
	anchor := f.cur.Anchor
	if !f.have {
		anchor = recs[0].Num
	}
	commits := make([]store.Commit, len(recs))
	var cur store.Cursor
	for i, rec := range recs {
		cur = store.Cursor{Anchor: anchor, Position: rec.Num, Head: h}
		c := cur
		commits[i] = store.Commit{Chain: f.cfg.Chain, Block: rec, Forward: &c}
		if i == 0 {
			commits[i].NewGap = gap
		}
	}
	if err := f.cfg.Store.CommitBlocks(ctx, commits); err != nil {
		return err
	}
	metrics.BlocksCommitted.WithLabelValues(f.cfg.Chain, "forward").Add(float64(len(commits)))
	metrics.ForwardPosition.WithLabelValues(f.cfg.Chain).Set(float64(cur.Position))
	f.cur, f.have, f.started = cur, true, true
	// Promotion depends only on the head, so it runs once per new head.
	if c := f.cfg.Confirmations; c > 0 && h >= c && h != f.promotedAt {
		if err := f.cfg.Store.PromoteConfirmed(ctx, f.cfg.Chain, h-c); err != nil {
			return err
		}
		f.promotedAt = h
	}
	return nil
}

// reorg walks back from the cursor to the newest block whose stored hash the
// source still serves, deletes everything above it and resumes from there.
func (f *Follower) reorg(ctx context.Context) error {
	pos := f.cur.Position
	for k := pos; k > 0 && pos-k < f.cfg.ReorgDepth; k-- {
		stored, err := f.cfg.Store.BlockHash(ctx, f.cfg.Chain, k)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		b, err := f.cfg.Source.Block(ctx, k)
		if err != nil {
			return fmt.Errorf("ingest: reorg search at %d: %w", k, err)
		}
		if !bytes.Equal(b.Hash, stored) {
			continue
		}
		if err := f.cfg.Store.Rollback(ctx, f.cfg.Chain, k); err != nil {
			return err
		}
		f.log().Warn("reorg", "chain", f.cfg.Chain, "from", pos, "ancestor", k)
		f.cur.Position = k
		return nil
	}
	return fmt.Errorf("ingest: %s reorg deeper than %d blocks below %d", f.cfg.Chain, f.cfg.ReorgDepth, pos)
}

func (f *Follower) role() string {
	if f.cfg.GapsOnly {
		return "backfill"
	}
	return "forward"
}

func (f *Follower) log() *slog.Logger {
	if f.cfg.Log != nil {
		return f.cfg.Log
	}
	return slog.Default()
}

// fetch returns the ledger record of block num; a skipped slot is a record
// without a block.
func (f *Follower) fetch(ctx context.Context, src Source, num uint64) (store.Block, error) {
	rec := store.Block{Num: num, Source: f.cfg.SourceName}
	b, err := src.Block(ctx, num)
	if errors.Is(err, ErrSkipped) {
		rec.Status = store.StatusSkipped
		return rec, nil
	}
	if err != nil {
		return rec, err
	}
	rec.Hash, rec.ParentHash, rec.Time, rec.TxCount, rec.Payload = b.Hash, b.ParentHash, b.Time, b.TxCount, b.Payload
	rec.Status = store.StatusConfirmed
	if c := f.cfg.Confirmations; c > 0 && f.head-num < c {
		rec.Status = store.StatusPending
	}
	return rec, nil
}

// Run steps until ctx is cancelled, logging failures; a forward follower
// with a ledger retention also prunes expired partitions and ledger rows at
// start and hourly. It returns nil on cancellation.
func (f *Follower) Run(ctx context.Context) error {
	var pruned time.Time
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		if !f.cfg.GapsOnly && f.cfg.LedgerRetention > 0 && time.Since(pruned) >= time.Hour {
			now := time.Now()
			var raw time.Time // zero: raw blocks are kept
			if f.cfg.RawRetention > 0 {
				raw = now.Add(-f.cfg.RawRetention)
			}
			if err := f.cfg.Store.Prune(ctx, raw, now.Add(-f.cfg.LedgerRetention)); err != nil && ctx.Err() == nil {
				f.log().Error("prune", "chain", f.cfg.Chain, "err", err)
			}
			pruned = now
		}
		wait, err := f.Step(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			metrics.StepErrors.WithLabelValues(f.role(), f.cfg.Chain).Inc()
			f.log().Warn("ingest step failed", "chain", f.cfg.Chain, "role", f.role(), "retry_in", wait.String(), "err", err)
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}
