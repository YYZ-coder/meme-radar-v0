package ingest_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/ingest"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

// sim is a simulated chain. Block n of fork f has hash (f, n) and points to
// the hash of block n-1 on the fork that holds it.
type sim struct {
	head    uint64
	skipped map[uint64]bool
	forkAt  uint64 // blocks >= forkAt belong to fork 1
	err     error  // returned by every call when set
	fetched []uint64
}

func (s *sim) fork(n uint64) byte {
	if s.forkAt != 0 && n >= s.forkAt {
		return 1
	}
	return 0
}

func (s *sim) hash(n uint64) []byte {
	h := make([]byte, 9)
	h[0] = s.fork(n)
	binary.BigEndian.PutUint64(h[1:], n)
	return h
}

func (s *sim) Head(context.Context) (uint64, error) { return s.head, s.err }

func (s *sim) Block(_ context.Context, n uint64) (ingest.Block, error) {
	if s.err != nil {
		return ingest.Block{}, s.err
	}
	s.fetched = append(s.fetched, n)
	if n > s.head {
		return ingest.Block{}, ingest.ErrNotAvailable
	}
	if s.skipped[n] {
		return ingest.Block{}, ingest.ErrSkipped
	}
	return ingest.Block{Num: n, Hash: s.hash(n), ParentHash: s.hash(n - 1),
		Time:    time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Second),
		TxCount: 1, Payload: []byte(fmt.Sprintf(`{"n":%d}`, n))}, nil
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, testdb.New(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func step(t *testing.T, f *ingest.Follower) time.Duration {
	t.Helper()
	wait, err := f.Step(context.Background())
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	return wait
}

func gaps(t *testing.T, s *store.Store, chain string) string {
	t.Helper()
	gs, err := s.OpenGaps(context.Background(), chain)
	if err != nil {
		t.Fatalf("OpenGaps: %v", err)
	}
	out := ""
	for _, g := range gs {
		out += fmt.Sprintf("[%d-%d %s]", g.From, g.To, g.Reason)
	}
	return out
}

func status(t *testing.T, s *store.Store, chain string, n uint64) string {
	t.Helper()
	st, err := s.BlockStatus(context.Background(), chain, n)
	if errors.Is(err, store.ErrNotFound) {
		return "missing"
	}
	if err != nil {
		t.Fatalf("BlockStatus(%d): %v", n, err)
	}
	return st
}

func solanaFollower(s *store.Store, src ingest.Source) *ingest.Follower {
	return ingest.NewFollower(ingest.Config{Chain: "solana", Source: src, SourceName: "sim", Store: s, FollowHead: true})
}

func TestFollowHeadJumpsToNewestAndRecordsGaps(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	chain := &sim{head: 100, skipped: map[uint64]bool{106: true}}
	f := solanaFollower(s, chain)

	step(t, f) // fresh start: the head block becomes the anchor
	if c, err := s.ForwardCursor(ctx, "solana"); err != nil || c.Anchor != 100 || c.Position != 100 {
		t.Fatalf("after first step cursor = %+v, %v; want anchor and position 100", c, err)
	}
	if g := gaps(t, s, "solana"); g != "" {
		t.Fatalf("fresh start gaps = %s; want none", g)
	}

	chain.head = 105
	step(t, f) // the newest block first; 101..104 are jumped over
	if got := status(t, s, "solana", 105); got != store.StatusConfirmed {
		t.Fatalf("block 105 status = %s; want confirmed", got)
	}
	if g := gaps(t, s, "solana"); g != "[101-104 rate_limited]" {
		t.Fatalf("gaps = %s; want [101-104 rate_limited]", g)
	}

	chain.head = 106
	step(t, f) // 106 is a skipped slot: processed, no block
	if got := status(t, s, "solana", 106); got != store.StatusSkipped {
		t.Fatalf("slot 106 status = %s; want skipped", got)
	}
	if c, _ := s.ForwardCursor(ctx, "solana"); c.Position != 106 || c.Head != 106 {
		t.Fatalf("cursor = %+v; want position and head 106", c)
	}

	// A restarted follower records the downtime range as a startup gap.
	chain.head = 110
	step(t, solanaFollower(s, chain))
	if g := gaps(t, s, "solana"); g != "[107-109 startup][101-104 rate_limited]" {
		t.Fatalf("after restart gaps = %s; want [107-109 startup][101-104 rate_limited]", g)
	}
	if got := status(t, s, "solana", 110); got != store.StatusConfirmed {
		t.Fatalf("block 110 status = %s; want confirmed", got)
	}
}

func evmFollower(s *store.Store, src ingest.Source, backfill uint64) *ingest.Follower {
	return ingest.NewFollower(ingest.Config{Chain: "base", Source: src, SourceName: "sim", Store: s,
		Confirmations: 3, ReorgDepth: 8, Backfill: backfill})
}

func TestFollowInOrderWithConfirmations(t *testing.T) {
	s := newStore(t)
	chain := &sim{head: 50}
	f := evmFollower(s, chain, 0)
	step(t, f)
	chain.head = 53
	for i := 0; i < 3; i++ {
		step(t, f)
	}
	if want := []uint64{50, 51, 52, 53}; fmt.Sprint(chain.fetched) != fmt.Sprint(want) {
		t.Fatalf("fetched %v; want blocks in order %v", chain.fetched, want)
	}
	// Head 53 with 3 confirmations: 50 is confirmed, 51..53 still pending.
	for n, want := range map[uint64]string{50: store.StatusConfirmed, 51: store.StatusPending, 53: store.StatusPending} {
		if got := status(t, s, "base", n); got != want {
			t.Errorf("block %d status = %s; want %s", n, got, want)
		}
	}
	if g := gaps(t, s, "base"); g != "" {
		t.Errorf("gaps = %s; want none for in-order following", g)
	}
	if wait := step(t, f); wait < 0 || len(chain.fetched) != 4 {
		t.Errorf("caught-up step fetched %v and waits %s; want no fetch", chain.fetched, wait)
	}
}

func TestReorgRollsBackToCommonAncestor(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	chain := &sim{head: 1}
	f := evmFollower(s, chain, 0)
	step(t, f)
	chain.head = 10
	for i := 0; i < 9; i++ {
		step(t, f)
	}
	oldTen, _ := s.BlockHash(ctx, "base", 10)
	// Blocks 8.. are replaced by a fork; 11 points at the new 10.
	chain.forkAt, chain.head = 8, 11
	for i := 0; i < 6 && len(chain.fetched) < 20; i++ {
		step(t, f)
		if c, _ := s.ForwardCursor(ctx, "base"); c.Position == 11 {
			break
		}
	}
	for _, n := range []uint64{8, 10, 11} {
		got, err := s.BlockHash(ctx, "base", n)
		if err != nil || string(got) != string(chain.hash(n)) {
			t.Errorf("block %d hash = %x, %v; want fork hash %x", n, got, err, chain.hash(n))
		}
	}
	if got, _ := s.BlockHash(ctx, "base", 7); string(got) != string(chain.hash(7)) {
		t.Errorf("block 7 hash = %x; want unchanged ancestor %x", got, chain.hash(7))
	}
	if newTen, _ := s.BlockHash(ctx, "base", 10); string(newTen) == string(oldTen) {
		t.Errorf("block 10 still has the orphaned hash %x", oldTen)
	}
	raw, err := s.RawBlock(ctx, "base", 9)
	if err != nil || string(raw) != `{"n":9}` {
		t.Errorf("raw block 9 = %s, %v; want the refetched payload", raw, err)
	}
}

func TestBackfillFillsWindowTopDownWhenIdle(t *testing.T) {
	s := newStore(t)
	chain := &sim{head: 100}
	f := evmFollower(s, chain, 5)
	step(t, f)
	if g := gaps(t, s, "base"); g != "[95-99 startup]" {
		t.Fatalf("fresh start gaps = %s; want [95-99 startup]", g)
	}
	step(t, f)
	step(t, f)
	chain.head = 101 // forward work goes first
	step(t, f)
	for i := 0; i < 4; i++ {
		step(t, f)
	}
	// With head 101 the window is 96..101: block 95 has aged out.
	if want := "[100 99 98 101 97 96]"; fmt.Sprint(chain.fetched) != want {
		t.Fatalf("fetched %v; want %s", chain.fetched, want)
	}
	if g := gaps(t, s, "base"); g != "[95-95 startup]" {
		t.Fatalf("gaps = %s; want only [95-95 startup] outside the window", g)
	}
	step(t, f)
	if len(chain.fetched) != 6 {
		t.Fatalf("idle step fetched %v; want nothing left to do", chain.fetched)
	}

	// Restarted after 20 blocks of downtime, more than the 5-block window:
	// jump to the head and fill only the window.
	chain.head, chain.fetched = 121, nil
	f = evmFollower(s, chain, 5)
	for i := 0; i < 8; i++ {
		step(t, f)
	}
	if want := "[121 120 119 118 117 116]"; fmt.Sprint(chain.fetched) != want {
		t.Fatalf("after restart fetched %v; want %s", chain.fetched, want)
	}
	if g := gaps(t, s, "base"); g != "[102-115 startup][95-95 startup]" {
		t.Fatalf("after restart gaps = %s; want [102-115 startup][95-95 startup] left outside the window", g)
	}
}

func TestNoBackfillLeavesGapsOpen(t *testing.T) {
	s := newStore(t)
	chain := &sim{head: 10}
	f := solanaFollower(s, chain)
	step(t, f)
	chain.head = 14
	step(t, f)
	step(t, f)
	if fmt.Sprint(chain.fetched) != "[10 14]" || gaps(t, s, "solana") != "[11-13 rate_limited]" {
		t.Fatalf("fetched %v gaps %s; want [10 14] and the gap left open", chain.fetched, gaps(t, s, "solana"))
	}
}

type limited struct{}

func (limited) Error() string             { return "429 too many requests" }
func (limited) RetryAfter() time.Duration { return 10 * time.Second }

func TestSourceErrorsAreRecordedAndBackedOff(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	chain := &sim{head: 5}
	f := evmFollower(s, chain, 0)
	step(t, f)
	chain.head = 6

	chain.err = limited{}
	wait, err := f.Step(ctx)
	if err == nil || wait != 10*time.Second {
		t.Fatalf("rate-limited Step = %s, %v; want an error and the 10s Retry-After", wait, err)
	}
	if kind, _, _ := s.ForwardError(ctx, "base"); kind != "rate_limited" {
		t.Fatalf("cursor error kind = %q; want rate_limited", kind)
	}

	chain.err = errors.New("connection reset")
	var waits []time.Duration
	for i := 0; i < 7; i++ {
		wait, err := f.Step(ctx)
		if err == nil {
			t.Fatalf("Step %d succeeded; want the source error", i)
		}
		waits = append(waits, wait)
	}
	if want := "[1s 2s 4s 8s 16s 30s 30s]"; fmt.Sprint(waits) != want {
		t.Fatalf("backoff waits = %v; want %s", waits, want)
	}
	if kind, msg, _ := s.ForwardError(ctx, "base"); kind != "error" || msg == "" {
		t.Fatalf("cursor error = %q %q; want kind error with a message", kind, msg)
	}

	chain.err = nil
	step(t, f)
	if kind, _, _ := s.ForwardError(ctx, "base"); kind != "" {
		t.Fatalf("cursor error after recovery = %q; want cleared", kind)
	}
	chain.err = errors.New("again")
	if wait, _ := f.Step(ctx); wait != time.Second {
		t.Fatalf("first wait after recovery = %s; want backoff reset to 1s", wait)
	}
}

func TestRunFollowsUntilCancelled(t *testing.T) {
	s := newStore(t)
	chain := &sim{head: 1}
	f := evmFollower(s, chain, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := s.ForwardCursor(context.Background(), "base")
		if err == nil && c.Position == 1 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("Run returned %v before following block 1", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not commit block 1 within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after cancel = %v; want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop within 5s of cancellation")
	}
}

func TestRestartResumesFromStoredProgress(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	chain := &sim{head: 20}
	f := evmFollower(s, chain, 10)
	step(t, f) // 20, startup gap 10..19
	step(t, f) // fills 19
	chain.head = 23
	step(t, f) // 21
	// Interrupted here. A new process reads the cursor and the open gap.
	chain.head, chain.fetched = 25, nil
	f = evmFollower(s, chain, 10)
	for i := 0; i < 6; i++ {
		step(t, f)
	}
	if want := "[22 23 24 25 18 17]"; fmt.Sprint(chain.fetched) != want {
		t.Fatalf("after restart fetched %v; want %s (resume at 22, then continue the gap below 19)", chain.fetched, want)
	}
	if c, err := s.ForwardCursor(ctx, "base"); err != nil || c.Anchor != 20 || c.Position != 25 {
		t.Fatalf("cursor = %+v, %v; want anchor 20 kept and position 25", c, err)
	}
	// 17..19 were filled; 15..16 are still inside the window (15..25) and
	// 10..14 have aged out, but the range stays recorded as one open gap.
	if g := gaps(t, s, "base"); g != "[10-16 startup]" {
		t.Fatalf("gaps = %s; want [10-16 startup]", g)
	}
}

// barrier is a simulated chain whose Block calls return only once n of them
// are in flight together, so sequential fetching times out.
type barrier struct {
	mu      sync.Mutex
	sim     *sim
	n       int
	waiting int
	release chan struct{}
}

func (b *barrier) Head(ctx context.Context) (uint64, error) { return b.sim.Head(ctx) }

func (b *barrier) Block(ctx context.Context, num uint64) (ingest.Block, error) {
	b.mu.Lock()
	b.waiting++
	if b.waiting == b.n {
		close(b.release)
	}
	release := b.release
	b.mu.Unlock()
	select {
	case <-release:
	case <-time.After(2 * time.Second):
		return ingest.Block{}, fmt.Errorf("block %d: fewer than %d concurrent fetches", num, b.n)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sim.Block(ctx, num)
}

func TestPrefetchFetchesConcurrentlyAndCommitsInOrder(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	chain := &sim{head: 10}
	f := ingest.NewFollower(ingest.Config{Chain: "bsc", Source: chain, SourceName: "sim", Store: s,
		Confirmations: 3, ReorgDepth: 8, Prefetch: 4})
	step(t, f)
	chain.head = 14
	f2 := ingest.NewFollower(ingest.Config{Chain: "bsc", Source: &barrier{sim: chain, n: 4, release: make(chan struct{})},
		SourceName: "sim", Store: s, Confirmations: 3, ReorgDepth: 8, Prefetch: 4})
	step(t, f2) // one step: 11..14 fetched together, committed in order
	if c, err := s.ForwardCursor(ctx, "bsc"); err != nil || c.Position != 14 {
		t.Fatalf("cursor after one prefetch step = %+v, %v; want position 14", c, err)
	}
	for n := uint64(11); n <= 14; n++ {
		if h, err := s.BlockHash(ctx, "bsc", n); err != nil || string(h) != string(chain.hash(n)) {
			t.Errorf("block %d hash = %x, %v; want %x", n, h, err, chain.hash(n))
		}
	}
}

func TestPrefetchFillsGapTopDownConcurrently(t *testing.T) {
	s := newStore(t)
	chain := &sim{head: 100}
	step(t, ingest.NewFollower(ingest.Config{Chain: "bsc", Source: chain, SourceName: "sim", Store: s,
		Confirmations: 3, ReorgDepth: 8, Backfill: 5})) // 100, gap 95..99
	f := ingest.NewFollower(ingest.Config{Chain: "bsc", Source: &barrier{sim: chain, n: 3, release: make(chan struct{})},
		SourceName: "sim", Store: s, Confirmations: 3, ReorgDepth: 8, Backfill: 5, Prefetch: 3})
	step(t, f) // caught up: 99, 98, 97 fetched together and filled in that order
	if g := gaps(t, s, "bsc"); g != "[95-96 startup]" {
		t.Fatalf("gaps after one prefetch fill step = %s; want [95-96 startup]", g)
	}
}

func TestBackfillUsesItsOwnSource(t *testing.T) {
	s := newStore(t)
	forward, older := &sim{head: 100}, &sim{head: 100}
	f := ingest.NewFollower(ingest.Config{Chain: "bsc", Source: forward, BackfillSource: older, SourceName: "sim", Store: s,
		Confirmations: 3, ReorgDepth: 8, Backfill: 3})
	for i := 0; i < 4; i++ {
		step(t, f) // 100 forward, then 99, 98, 97 from the gap
	}
	if fmt.Sprint(forward.fetched) != "[100]" || fmt.Sprint(older.fetched) != "[99 98 97]" {
		t.Fatalf("forward source fetched %v, backfill source %v; want [100] and [99 98 97]", forward.fetched, older.fetched)
	}
}

func TestBackfillRoleFillsTheWindowWhileForwardOnlyFollows(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	chain := &sim{head: 100}
	fwd := ingest.NewFollower(ingest.Config{Chain: "base", Source: chain, SourceName: "sim", Store: s,
		Confirmations: 3, ReorgDepth: 8, Backfill: 5, SkipFill: true})
	for i := 0; i < 3; i++ {
		step(t, fwd)
	}
	// The forward follower leaves its startup gap to the backfill role.
	if fmt.Sprint(chain.fetched) != "[100]" || gaps(t, s, "base") != "[95-99 startup]" {
		t.Fatalf("forward fetched %v gaps %s; want only [100] and [95-99 startup] open", chain.fetched, gaps(t, s, "base"))
	}
	// A 10-block window adds [90-94] below what is already known, then fills newest first.
	old := &sim{head: 100}
	back := ingest.NewFollower(ingest.Config{Chain: "base", Source: old, SourceName: "sim", Store: s,
		Confirmations: 3, Backfill: 10, GapsOnly: true})
	for i := 0; i < 12; i++ {
		step(t, back)
	}
	if want := "[99 98 97 96 95 94 93 92 91 90]"; fmt.Sprint(old.fetched) != want || gaps(t, s, "base") != "" {
		t.Fatalf("backfill fetched %v gaps %s; want %s and no open gaps", old.fetched, gaps(t, s, "base"), want)
	}
	for _, n := range []uint64{90, 97} {
		if st := status(t, s, "base", n); st != store.StatusConfirmed {
			t.Errorf("block %d = %s; want confirmed", n, st)
		}
	}
	if c, err := s.ForwardCursor(ctx, "base"); err != nil || c.Position != 100 {
		t.Errorf("forward cursor = %+v, %v; want 100: backfill does not move it", c, err)
	}
	chain.head = 101
	step(t, fwd)
	if c, _ := s.ForwardCursor(ctx, "base"); c.Position != 101 {
		t.Errorf("forward cursor after head 101 = %d; want 101", c.Position)
	}
}

func TestBackfillYieldsWhileForwardLags(t *testing.T) {
	s := newStore(t)
	chain := &sim{head: 100}
	fwd := ingest.NewFollower(ingest.Config{Chain: "base", Source: chain, SourceName: "sim", Store: s, Confirmations: 3, Backfill: 5, SkipFill: true})
	step(t, fwd)     // forward at 100, startup gap [95-99]
	chain.head = 200 // forward now 100 blocks behind
	old := &sim{head: 200}
	back := ingest.NewFollower(ingest.Config{Chain: "base", Source: old, SourceName: "sim", Store: s, Backfill: 300, GapsOnly: true, PauseLag: 50})
	for i := 0; i < 3; i++ {
		step(t, back)
	}
	if len(old.fetched) != 0 {
		t.Fatalf("backfill fetched %v while forward lags 100 blocks; want it to wait", old.fetched)
	}
	for i := 0; i < 60; i++ { // forward catches up to within the limit
		step(t, fwd)
	}
	back = ingest.NewFollower(ingest.Config{Chain: "base", Source: old, SourceName: "sim", Store: s, Backfill: 300, GapsOnly: true, PauseLag: 50})
	step(t, back)
	if len(old.fetched) == 0 {
		t.Fatalf("backfill still waiting with forward lag %s", gaps(t, s, "base"))
	}
}

func TestFollowersReportBlocksAndHead(t *testing.T) {
	s := newStore(t)
	chain := &sim{head: 50}
	fwd := ingest.NewFollower(ingest.Config{Chain: "bsc", Source: chain, SourceName: "sim", Store: s, Backfill: 3, SkipFill: true})
	before := testutil.ToFloat64(metrics.BlocksCommitted.WithLabelValues("bsc", "forward"))
	step(t, fwd)
	chain.head = 52
	step(t, fwd)
	step(t, fwd)
	if got := testutil.ToFloat64(metrics.BlocksCommitted.WithLabelValues("bsc", "forward")) - before; got != 3 {
		t.Errorf("forward blocks reported = %v; want 3 (50, 51, 52)", got)
	}
	if h, p := testutil.ToFloat64(metrics.ChainHead.WithLabelValues("bsc", "forward")), testutil.ToFloat64(metrics.ForwardPosition.WithLabelValues("bsc")); h != 52 || p != 52 {
		t.Errorf("head %v position %v; want 52 52", h, p)
	}
	back := ingest.NewFollower(ingest.Config{Chain: "bsc", Source: &sim{head: 52}, SourceName: "sim", Store: s, Backfill: 6, GapsOnly: true})
	before = testutil.ToFloat64(metrics.BlocksCommitted.WithLabelValues("bsc", "backfill"))
	for i := 0; i < 5; i++ {
		step(t, back)
	}
	if got := testutil.ToFloat64(metrics.BlocksCommitted.WithLabelValues("bsc", "backfill")) - before; got != 4 {
		t.Errorf("backfill blocks reported = %v; want 4 (49, 48, 47, 46)", got)
	}
}

func TestForwardJumpsToHeadWhenLagExceedsMaxLag(t *testing.T) {
	s := newStore(t)
	chain := &sim{head: 100}
	// A 7-day backfill window would never trigger a jump; MaxLag does (BSC: the
	// free endpoint stops serving receipts ~8k-12k blocks back, 2026-10-24).
	f := ingest.NewFollower(ingest.Config{Chain: "bsc", Source: chain, SourceName: "sim", Store: s, Backfill: 1000, SkipFill: true, MaxLag: 50})
	step(t, f) // forward at 100, startup gap [0... below]
	chain.head = 140
	step(t, f) // lag 40 <= 50: in order
	if fmt.Sprint(chain.fetched) != "[100 101]" {
		t.Fatalf("within MaxLag fetched %v; want [100 101] in order", chain.fetched)
	}
	chain.head, chain.fetched = 300, nil
	step(t, f) // lag 199 > 50: jump to the head
	if fmt.Sprint(chain.fetched) != "[300]" {
		t.Fatalf("beyond MaxLag fetched %v; want [300]", chain.fetched)
	}
	if g := gaps(t, s, "bsc"); !strings.Contains(g, "[102-299 rate_limited]") {
		t.Fatalf("gaps = %s; want [102-299 rate_limited] left to backfill", g)
	}
	// Without MaxLag the backfill window still decides (old behaviour).
	s2 := newStore(t)
	c2 := &sim{head: 100}
	f2 := ingest.NewFollower(ingest.Config{Chain: "bsc", Source: c2, SourceName: "sim", Store: s2, Backfill: 1000, SkipFill: true})
	step(t, f2)
	c2.head, c2.fetched = 300, nil
	step(t, f2)
	if fmt.Sprint(c2.fetched) != "[101]" {
		t.Fatalf("without MaxLag fetched %v; want [101] in order", c2.fetched)
	}
}
