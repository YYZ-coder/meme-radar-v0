package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
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
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='40s'`); e != nil {
		panic(e)
	}
	var at time.Time
	if e = tx.QueryRow(ctx, `SELECT date_trunc('minute',least(now(),max(to_time))) FROM coverage_ranges WHERE chain='solana'`).Scan(&at); e != nil {
		panic(e)
	}
	fmt.Printf("mode=REPEATABLE_READ_READ_ONLY chain=solana window=24h as_of=%s H3_H4_H5_not_deployed=true seed_join_only_change=true\n", at.UTC().Format(time.RFC3339))
	start := time.Now()
	r, e := tx.Query(ctx, read("testdata/expect/evidence/h4-audit/sparse.sql"), "solana", at.Add(-24*time.Hour), at, []int64{86400}, int64(1), int64(1), "24h")
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
	b, e := json.Marshal(values)
	if e != nil {
		panic(e)
	}
	fmt.Printf("rows=%d query_decode=%s fill=%s total=%s serialized_bytes=%d heap_alloc_bytes=%d audit_peak_rss_bytes_darwin=%d write=NOT_RUN_CLOUD_READ_ONLY\n", len(values), total-fill, fill, total, len(b), mem.Alloc, usage.Maxrss)
}
