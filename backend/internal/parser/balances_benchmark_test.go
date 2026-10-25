package parser_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/klauspost/compress/zstd"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parser"
	"github.com/meme-radar/radar/internal/store"
)

// TestBalanceBackfillBenchmarkReadOnly samples retained cloud blocks, then
// measures the actual balance pass and accumulator in a throwaway LOCAL DB.
// Cloud access is session-enforced read-only; skipped in ordinary tests/CI.
func TestBalanceBackfillBenchmarkReadOnly(t *testing.T) {
	url := os.Getenv("RADAR_H5_READONLY_DATABASE_URL")
	if url == "" {
		t.Skip("manual cloud read-only throughput measurement")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal("parse read-only cloud config failed")
	}
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.RuntimeParams["statement_timeout"] = "30000"
	cfg.RuntimeParams["TimeZone"] = "UTC"
	cloud, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("connect read-only cloud failed")
	}
	defer cloud.Close(ctx)
	if got := query(t, cloud, "SHOW default_transaction_read_only"); got != "on" {
		t.Fatal("cloud read-only setting missing")
	}
	t.Logf("H5 measured_utc=%s", time.Now().UTC().Format(time.RFC3339))
	var rtts []time.Duration
	for range 3 {
		start := time.Now()
		if query(t, cloud, "SELECT 1::text") != "1" {
			t.Fatal("cloud ping failed")
		}
		rtts = append(rtts, time.Since(start))
	}
	slices.Sort(rtts)
	rtt := rtts[1]
	local, inspect := setup(t)
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	for _, chain := range []string{"solana", "base", "bsc"} {
		t.Run(chain, func(t *testing.T) {
			var total, eligible int64
			if err := cloud.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status<>'skipped'),count(*) FILTER(WHERE status<>'skipped' AND parsed_version IS NOT NULL) FROM chain_blocks WHERE chain=$1`, chain).Scan(&total, &eligible); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			rows, err := cloud.Query(ctx, `WITH bounds AS (SELECT min(block_num) AS lo,max(block_num) AS hi FROM chain_blocks WHERE chain=$1 AND status<>'skipped' AND parsed_version IS NOT NULL),
            targets AS (SELECT lo+((hi-lo)*i/9) AS n FROM bounds CROSS JOIN generate_series(0,9) i)
            SELECT DISTINCT c.block_num,c.block_time,c.block_hash,r.payload FROM targets t
            JOIN LATERAL (SELECT block_num,block_time,block_hash FROM chain_blocks WHERE chain=$1 AND block_num>=t.n AND status<>'skipped' AND parsed_version IS NOT NULL ORDER BY block_num LIMIT 1) c ON true
            JOIN raw_blocks r ON r.chain=$1 AND r.block_num=c.block_num AND r.block_hash=c.block_hash AND r.block_time=c.block_time`, chain)
			if err != nil {
				t.Fatal(err)
			}
			type sample struct {
				num              uint64
				at               time.Time
				hash, compressed []byte
			}
			blocks, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (sample, error) {
				var b sample
				var n int64
				err := r.Scan(&n, &b.at, &b.hash, &b.compressed)
				b.num = uint64(n)
				return b, err
			})
			if err != nil {
				t.Fatal(err)
			}
			readDuration := time.Since(started)
			if len(blocks) < 3 {
				t.Fatal("too few retained samples")
			}
			rows, err = cloud.Query(ctx, `SELECT address FROM tokens WHERE chain=$1`, chain)
			if err != nil {
				t.Fatal(err)
			}
			addresses, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
			if err != nil {
				t.Fatal(err)
			}
			var commits []store.Commit
			var main []store.Parsed
			var compressedBytes int
			for _, b := range blocks {
				payload, err := dec.DecodeAll(b.compressed, nil)
				if err != nil {
					t.Fatal(err)
				}
				compressedBytes += len(b.compressed)
				commits = append(commits, store.Commit{Chain: chain, Block: store.Block{Num: b.num, Time: b.at, Hash: b.hash, Status: store.StatusConfirmed, Payload: payload, Source: "read-only-cloud-sample"}})
				main = append(main, store.Parsed{Chain: chain, Num: b.num, Time: b.at, Version: 1})
			}
			// Fixture setup stays outside timing. Main facts use the formal writer;
			// all tracked tokens are loaded once in this single benchmark batch.
			for _, a := range addresses {
				main[0].Facts.Tokens = append(main[0].Facts.Tokens, parse.Token{Address: a})
			}
			if err := local.CommitBlocks(ctx, commits); err != nil {
				t.Fatal(err)
			}
			if err := local.ApplyParsedMany(ctx, main); err != nil {
				t.Fatal(err)
			}
			family := "evm"
			if chain == "solana" {
				family = "solana"
			}
			pass := parser.NewBalances(parser.BalanceConfig{Chain: chain, Family: family, Store: local, Batch: len(blocks)})
			started = time.Now()
			if _, err := pass.Step(ctx); err != nil {
				t.Fatal(err)
			}
			passDuration := time.Since(started)
			started = time.Now()
			if err := compute.NewBalances(local, []string{chain}).Step(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			computeDuration := time.Since(started)
			// Independent cloud-data audit, outside throughput timing.
			var publicBlocks []map[string]any
			var tracked []string
			for _, c := range commits {
				publicBlocks = append(publicBlocks, map[string]any{"num": c.Block.Num, "payload": json.RawMessage(c.Block.Payload)})
			}
			for _, a := range addresses {
				tracked = append(tracked, hex.EncodeToString(a))
			}
			input, err := json.Marshal(map[string]any{"chain": chain, "blocks": publicBlocks, "tracked": tracked})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, "python3", "../../testdata/expect/check_h5_deltas.py")
			cmd.Stdin = bytes.NewReader(input)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("independent Python decoder: %v: %s", err, output)
			}
			var expected map[string][]string
			if err := json.Unmarshal(output, &expected); err != nil {
				t.Fatal(err)
			}
			for _, b := range blocks {
				rows, err := inspect.Query(ctx, `SELECT tx_index,ix_index,encode(token_address,'hex'),encode(owner,'hex'),delta::text FROM balance_deltas WHERE chain=$1 AND block_num=$2`, chain, int64(b.num))
				if err != nil {
					t.Fatal(err)
				}
				actual, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (string, error) {
					var tx, ix int
					var token, owner, amount string
					err := r.Scan(&tx, &ix, &token, &owner, &amount)
					return fmt.Sprintf("%d|%d|%s|%s|%s", tx, ix, token, owner, amount), err
				})
				if err != nil {
					t.Fatal(err)
				}
				slices.Sort(actual)
				want := expected[fmt.Sprint(b.num)]
				if !slices.Equal(actual, want) {
					t.Errorf("chain=%s block=%d Go rows=%d Python rows=%d (values differ)", chain, b.num, len(actual), len(want))
				}
				sum := sha256.Sum256([]byte(strings.Join(actual, "\n")))
				t.Logf("H5_AUDIT chain=%s block=%d rows=%d sha256=%x independent_python_match=%v", chain, b.num, len(actual), sum, slices.Equal(actual, want))
			}

			// Conservative model for default 50-block batches: measured sample
			// CPU/local SQL + cloud payload reads, plus token refresh and parse/
			// compute transaction round trips. Compute is charged once per batch.
			perBlock := (readDuration+passDuration+computeDuration)/time.Duration(len(blocks)) + 11*rtt/50
			estimated := perBlock * time.Duration(total)
			t.Logf("H5_MEASURE chain=%s blocks=%d eligible=%d samples=%d compressed_bytes=%d tracked=%d cloud_read=%s local_balance_pass=%s local_compute=%s cloud_rtt=%s default_batch=50 projected=%s", chain, total, eligible, len(blocks), compressedBytes, len(addresses), readDuration, passDuration, computeDuration, rtt, estimated)
		})
	}
}
