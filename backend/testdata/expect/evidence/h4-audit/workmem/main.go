package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func setting(ctx context.Context, tx pgx.Tx, value string) {
	_, err := tx.Exec(ctx, "SET LOCAL work_mem='"+value+"'")
	must(err)
}
func read(ctx context.Context, tx pgx.Tx, q string, args []any) (map[string][]byte, int64, time.Duration) {
	start := time.Now()
	rows, err := tx.Query(ctx, q, args...)
	must(err)
	defer rows.Close()
	out := map[string][]byte{}
	var size int64
	for rows.Next() {
		raw := rows.RawValues()
		if len(raw) != 37 {
			panic(fmt.Sprintf("got %d fields, want 37", len(raw)))
		}
		key := fmt.Sprintf("%x", raw[3])
		if _, exists := out[key]; exists {
			panic("duplicate token " + key)
		}
		var buf bytes.Buffer
		for _, v := range raw {
			length := int32(-1)
			if v != nil {
				length = int32(len(v))
			}
			must(binary.Write(&buf, binary.BigEndian, length))
			buf.Write(v)
			size += int64(len(v))
		}
		out[key] = append([]byte(nil), buf.Bytes()...)
	}
	must(rows.Err())
	return out, size, time.Since(start)
}
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c, err := pgx.Connect(ctx, os.Getenv("RADAR_DATABASE_URL"))
	if err != nil {
		panic("cloud readonly connection unavailable")
	}
	defer c.Close(ctx)
	tx, err := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	must(err)
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, "SET LOCAL statement_timeout='75s'")
	must(err)
	q := map[bool]string{}
	for _, long := range []bool{true, false} {
		name := "short"
		if long {
			name = "long"
		}
		b, err := os.ReadFile("testdata/expect/evidence/h4-audit/workmem/" + name + ".sql")
		must(err)
		q[long] = string(b)
	}
	cutoffs := map[string]time.Time{}
	for _, chain := range []string{"solana", "base", "bsc"} {
		var at time.Time
		must(tx.QueryRow(ctx, `SELECT date_trunc('minute',least(now(),max(to_time))) FROM coverage_ranges WHERE chain=$1`, chain).Scan(&at))
		cutoffs[chain] = at
	}
	fmt.Println("mode=REPEATABLE_READ_READ_ONLY; one snapshot; pre-E=4MB vs E=64MB; exact same SQL; undeployed H3/H4/H5/metadata represented by identical empty/unknown fields; no cloud writes; cold means first natural-cache execution, no forced clear")
	at := cutoffs["solana"]
	args := []any{"solana", at.Add(-24 * time.Hour), at, []int64{86400}, int64(1), int64(1), "24h"}
	for _, memory := range []string{"64MB", "4MB"} {
		setting(ctx, tx, memory)
		for round := 1; round <= 2; round++ {
			var raw []byte
			start := time.Now()
			must(tx.QueryRow(ctx, "EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) "+q[true], args...).Scan(&raw))
			var plans []map[string]any
			must(json.Unmarshal(raw, &plans))
			p := plans[0]["Plan"].(map[string]any)
			fmt.Printf("plan work_mem=%s round=%d chain=solana window=24h as_of=%s rows=%v server_execution_ms=%v planning_ms=%v client_explain_elapsed=%s shared_read=%v temp_read=%v temp_written=%v\n", memory, round, at.UTC().Format(time.RFC3339), p["Actual Rows"], plans[0]["Execution Time"], plans[0]["Planning Time"], time.Since(start), p["Shared Read Blocks"], p["Temp Read Blocks"], p["Temp Written Blocks"])
			must(os.WriteFile(fmt.Sprintf("testdata/expect/evidence/h4-39-workmem-%s-plan-%d.json", memory, round), raw, 0600))
		}
	}
	total := 0
	for _, chain := range []string{"solana", "base", "bsc"} {
		at := cutoffs[chain]
		for _, window := range []struct {
			name  string
			width time.Duration
		}{{"24h", 24 * time.Hour}, {"1h", time.Hour}, {"4h", 4 * time.Hour}, {"1m", time.Minute}, {"5m", 5 * time.Minute}} {
			args := []any{chain, at.Add(-window.width), at, []int64{int64(window.width / time.Second)}, int64(1), int64(1), window.name}
			setting(ctx, tx, "4MB")
			before, beforeBytes, beforeTime := read(ctx, tx, q[window.width >= time.Hour], args)
			setting(ctx, tx, "64MB")
			after, afterBytes, afterTime := read(ctx, tx, q[window.width >= time.Hour], args)
			diffs := 0
			for token, b := range before {
				if !bytes.Equal(b, after[token]) {
					diffs++
					if diffs <= 5 {
						fmt.Printf("DIFF chain=%s window=%s token=%s before=%x after=%x\n", chain, window.name, token, b, after[token])
					}
				}
			}
			keys := make([]string, 0, len(after))
			for key := range after {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			h := sha256.New()
			for _, key := range keys {
				h.Write(after[key])
			}
			fmt.Printf("compare chain=%s window=%s as_of=%s rows_before=%d rows_after=%d diffs=%d before_receive=%s after_receive=%s before_field_bytes=%d after_field_bytes=%d raw_row_sha256=%x\n", chain, window.name, at.UTC().Format(time.RFC3339), len(before), len(after), diffs, beforeTime, afterTime, beforeBytes, afterBytes, h.Sum(nil))
			if diffs != 0 || len(before) != len(after) {
				panic("full byte equivalence failed")
			}
			total += len(after)
		}
	}
	fmt.Printf("PASS full 37 fields, USD/native and all 20 spark points byte-identical for 3 chains x 5 windows; total_rows=%d; cloud writes=0\n", total)
}
