package parser_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/klauspost/compress/zstd"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parser"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

func setup(t *testing.T) (*store.Store, *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	url := testdb.New(t)
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return s, c
}

// recordedBlock is the full mainnet block of slot 460261066 (346
// transactions) recorded from getBlock, as ingest stores it.
func recordedBlock(t *testing.T) []byte {
	t.Helper()
	z, err := os.ReadFile("../ingest/testdata/solana_getblock_460261066.json.zst")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := zstd.NewReader(nil)
	defer d.Close()
	b, err := d.DecodeAll(z, nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct{ Result json.RawMessage }
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Result
}

func commit(t *testing.T, s *store.Store, num uint64, payload []byte) {
	t.Helper()
	b := store.Block{Num: num, Hash: []byte{1}, ParentHash: []byte{0}, Time: time.Unix(1792719992, 0),
		Status: store.StatusConfirmed, TxCount: 1, Payload: payload, Source: "test"}
	if err := s.Commit(context.Background(), store.Commit{Chain: "solana", Block: b}); err != nil {
		t.Fatal(err)
	}
}

func query(t *testing.T, c *pgx.Conn, sql string) string {
	t.Helper()
	var v string
	if err := c.QueryRow(context.Background(), sql).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func solanaParser(s *store.Store, version int) *parser.Parser {
	return parser.New(parser.Config{Chain: "solana", Store: s, Decode: parser.Solana, Version: version, Batch: 10})
}

func TestStepParsesStoredSolanaBlock(t *testing.T) {
	ctx := context.Background()
	s, c := setup(t)
	commit(t, s, 460261066, recordedBlock(t))
	before := testutil.ToFloat64(metrics.BlocksParsed.WithLabelValues("solana"))
	if _, err := solanaParser(s, 1).Step(ctx); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got := testutil.ToFloat64(metrics.BlocksParsed.WithLabelValues("solana")) - before; got != 1 {
		t.Errorf("parsed blocks reported = %v; want 1", got)
	}
	// Expectations: the independent Python decoder over the same block
	// (backend/testdata/expect/expect_pump.py): 42 trades in 38 transactions, 27 pools
	// and tokens, and the SHA-256 of the sorted "tx/ix" trade keys.
	for sql, want := range map[string]string{
		"SELECT count(*)::text FROM trades":                                          "42",
		"SELECT count(*)::text FROM pools":                                           "27",
		"SELECT count(*)::text FROM tokens":                                          "27",
		"SELECT count(*)::text FROM decode_failures":                                 "0",
		"SELECT parsed_version || ' ' || hit_txs || ' ' || events FROM chain_blocks": "1 38 42",
	} {
		if got := query(t, c, sql); got != want {
			t.Errorf("%s = %s; want %s", sql, got, want)
		}
	}
	rows, _ := c.Query(ctx, "SELECT tx_index || '/' || ix_index FROM trades")
	keys, _ := pgx.CollectRows(rows, pgx.RowTo[string])
	slices.Sort(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, ",")))
	if got := hex.EncodeToString(sum[:])[:16]; got != "f929f7e5ccd30c13" {
		t.Errorf("trade keys hash = %s; want f929f7e5ccd30c13 (first keys %v)", got, keys[:min(3, len(keys))])
	}
	if got, _ := s.UnparsedBlocks(ctx, "solana", 10); len(got) != 0 {
		t.Errorf("unparsed after Step = %v; want none", got)
	}
}

func TestStepRecordsUndecodableBlockAndMovesOn(t *testing.T) {
	ctx := context.Background()
	s, c := setup(t)
	commit(t, s, 7, []byte(`{"transactions":[]}`)) // no blockTime
	before := testutil.ToFloat64(metrics.DecodeFailures.WithLabelValues("solana"))
	if _, err := solanaParser(s, 1).Step(ctx); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got := testutil.ToFloat64(metrics.DecodeFailures.WithLabelValues("solana")) - before; got != 1 {
		t.Errorf("decode failures reported = %v; want 1", got)
	}
	if got := query(t, c, "SELECT tx_index || ' ' || decoder FROM decode_failures"); got != "-1 block" {
		t.Errorf("decode_failures = %s; want a block-level failure (-1 block)", got)
	}
	if got := query(t, c, "SELECT parsed_version::text FROM chain_blocks"); got != "1" {
		t.Errorf("parsed_version = %s; want 1 so the block is not retried forever", got)
	}
}

func TestRunReparsesBlocksOfOlderVersions(t *testing.T) {
	s, c := setup(t)
	commit(t, s, 460261066, recordedBlock(t))
	if _, err := solanaParser(s, 1).Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- solanaParser(s, 2).Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for query(t, c, "SELECT coalesce(parsed_version::text, 'unparsed') FROM chain_blocks") != "2" {
		if time.Now().After(deadline) {
			t.Fatal("version 2 parser did not reparse the block within 10s")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v; want nil after cancel", err)
	}
	if got := query(t, c, "SELECT count(*)::text FROM trades"); got != "42" {
		t.Errorf("trades after reparse = %s; want 42 (replaced, not duplicated)", got)
	}
}

func TestStepRetriesTransientDecodeErrors(t *testing.T) {
	ctx := context.Background()
	s, c := setup(t)
	commit(t, s, 7, []byte(`{}`))
	flaky := func(context.Context, []byte) (parser.Decoded, error) {
		return parser.Decoded{}, fmt.Errorf("lookup: %w", parse.ErrRetry)
	}
	p := parser.New(parser.Config{Chain: "solana", Store: s, Decode: flaky, Version: 1, Batch: 10})
	if _, err := p.Step(ctx); !errors.Is(err, parse.ErrRetry) {
		t.Fatalf("Step err = %v; want the retryable error", err)
	}
	if got := query(t, c, "SELECT coalesce(parsed_version::text, 'unparsed') || ' ' || (SELECT count(*) FROM decode_failures) FROM chain_blocks"); got != "unparsed 0" {
		t.Fatalf("block after a retryable error = %s; want unparsed with no failure recorded", got)
	}
}

func TestStepDecodesBlocksConcurrently(t *testing.T) {
	ctx := context.Background()
	s, c := setup(t)
	for n := uint64(1); n <= 3; n++ {
		commit(t, s, n, []byte(`{}`))
	}
	var mu sync.Mutex
	waiting, release := 0, make(chan struct{})
	barrier := func(context.Context, []byte) (parser.Decoded, error) {
		mu.Lock()
		if waiting++; waiting == 3 {
			close(release)
		}
		mu.Unlock()
		select {
		case <-release:
			return parser.Decoded{}, nil
		case <-time.After(2 * time.Second):
			return parser.Decoded{}, errors.New("blocks were not decoded concurrently")
		}
	}
	p := parser.New(parser.Config{Chain: "solana", Store: s, Decode: barrier, Version: 1, Batch: 10, Concurrency: 3})
	if _, err := p.Step(ctx); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got := query(t, c, "SELECT count(*) FILTER (WHERE parsed_version = 1) || ' ' || (SELECT count(*) FROM decode_failures) FROM chain_blocks"); got != "3 0" {
		t.Fatalf("parsed blocks / failures = %s; want 3 0", got)
	}
}
