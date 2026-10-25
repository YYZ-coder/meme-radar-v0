package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var inflowColumns = []string{"projection_version", "chain", "window", "token_address", "symbol", "name", "image_url", "launch_platform", "launch_at",
	"price_usd", "price_native", "market_cap_usd", "market_cap_native", "price_change_pct", "price_change_pct_native", "liquidity_usd", "liquidity_native",
	"trades", "buys", "sells", "observed_trades", "observed_buys", "observed_sells", "coverage", "volume_usd", "volume_native", "net_inflow_usd", "net_inflow_native",
	"sm_holders", "sm_trades", "holders", "age_seconds", "dexes", "socials", "sparkline", "sparkline_native", "dev_holding_pct"}

func collectInflowRows(rows pgx.Rows, sparse bool) ([][]any, time.Duration, error) {
	defer rows.Close()
	var out [][]any
	var fillTime time.Duration
	for rows.Next() {
		values := make([]any, len(inflowColumns))
		targets := make([]any, len(values)+3)
		for i := range values {
			targets[i] = &values[i]
		}
		var usd, native []pgtype.Numeric
		var buckets []int32
		var seedUSD, seedNative pgtype.Numeric
		targets[34], targets[35] = &usd, &native
		targets[37], targets[38], targets[39] = &buckets, &seedUSD, &seedNative
		if err := rows.Scan(targets...); err != nil {
			return nil, fillTime, err
		}
		if sparse {
			started := time.Now()
			usd, native = fillInflowBuckets(seedUSD, seedNative, buckets, usd, native)
			fillTime += time.Since(started)
		}
		values[34], values[35] = usd, native
		out = append(out, values)
	}
	return out, fillTime, rows.Err()
}
func fillInflowBuckets(seedUSD, seedNative pgtype.Numeric, buckets []int32, usd, native []pgtype.Numeric) ([]pgtype.Numeric, []pgtype.Numeric) {
	outUSD, outNative := make([]pgtype.Numeric, 20), make([]pgtype.Numeric, 20)
	next := 0
	for bucket := int32(0); bucket < 20; bucket++ {
		if next < len(buckets) && buckets[next] == bucket {
			seedUSD, seedNative = usd[next], native[next]
			next++
		}
		outUSD[bucket], outNative[bucket] = seedUSD, seedNative
	}
	return outUSD, outNative
}

func read(p string) string {
	b, e := os.ReadFile(p)
	if e != nil {
		panic(e)
	}
	return string(b)
}
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	c, e := pgx.Connect(ctx, os.Getenv("RADAR_DATABASE_URL"))
	if e != nil {
		fmt.Println("connect failed")
		os.Exit(1)
	}
	defer c.Close(ctx)
	tx, e := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		panic(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='75s'`); e != nil {
		panic(e)
	}
	oldSQL, newSQL := read("testdata/expect/evidence/h4-audit/before-sparse.sql"), read("testdata/expect/evidence/h4-audit/sparse.sql")
	fmt.Println("mode=REPEATABLE_READ_READ_ONLY full_row_equivalence old_per_token_vs_sparse_go; H3/H4/H5 not deployed: empty smart copies and unknown holder/metadata fields in BOTH previews; no cloud writes")
	for _, chain := range []string{"solana", "base", "bsc"} {
		var at time.Time
		if e = tx.QueryRow(ctx, `SELECT date_trunc('minute',least(now(),max(to_time))) FROM coverage_ranges WHERE chain=$1`, chain).Scan(&at); e != nil {
			panic(e)
		}
		for _, w := range []struct {
			name  string
			width time.Duration
		}{{"1h", time.Hour}, {"4h", 4 * time.Hour}, {"24h", 24 * time.Hour}} {
			args := []any{chain, at.Add(-w.width), at, []int64{int64(w.width / time.Second)}, int64(1), int64(1), w.name}
			start := time.Now()
			r, e := tx.Query(ctx, newSQL, args...)
			if e != nil {
				panic(e)
			}
			values, fill, e := collectInflowRows(r, true)
			total := time.Since(start)
			if e != nil {
				panic(e)
			}
			var mem runtime.MemStats
			runtime.ReadMemStats(&mem)
			var usage syscall.Rusage
			syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
			fmt.Printf("sparse chain=%s window=%s as_of=%s rows=%d query_decode=%s fill=%s total=%s heap_alloc_bytes=%d peak_rss_bytes_darwin=%d write=NOT_RUN_CLOUD_READ_ONLY\n", chain, w.name, at.UTC().Format(time.RFC3339), len(values), total-fill, fill, total, mem.Alloc, usage.Maxrss)
			byKey := map[string][]byte{}
			for _, v := range values {
				b, e := json.Marshal(v)
				if e != nil {
					panic(e)
				}
				byKey[fmt.Sprintf("%x", v[3])] = b
			}
			values = nil
			runtime.GC()
			start = time.Now()
			r, e = tx.Query(ctx, oldSQL, args...)
			if e != nil {
				panic(e)
			}
			count, diff := 0, 0
			for r.Next() {
				v, e := r.Values()
				if e != nil {
					panic(e)
				}
				b, e := json.Marshal(v)
				if e != nil {
					panic(e)
				}
				key := fmt.Sprintf("%x", v[3])
				want, ok := byKey[key]
				if !ok || !bytes.Equal(b, want) {
					diff++
					if diff <= 5 {
						fmt.Printf("DIFF token=%s old=%s new=%s\n", key, b, want)
					}
				}
				count++
			}
			e = r.Err()
			r.Close()
			if e != nil {
				panic(e)
			}
			keys := make([]string, 0, len(byKey))
			for k := range byKey {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			h := sha256.New()
			for _, k := range keys {
				h.Write(byKey[k])
			}
			fmt.Printf("compare chain=%s window=%s old_rows=%d new_rows=%d diffs=%d old_elapsed=%s sha256=%x\n", chain, w.name, count, len(byKey), diff, time.Since(start), h.Sum(nil))
			if diff != 0 || count != len(byKey) {
				os.Exit(3)
			}
		}
	}
	fmt.Println("PASS full rows and all20 USD/native spark points identical for 3 chains x 3 long windows in ONE readonly snapshot")
}
