// Read-only live-wait sampling complements EXPLAIN: a different connection
// observes this query's wait events, while all data reads share one RR snapshot.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

type sample struct {
	AtMS    int64  `json:"at_ms"`
	QueryMS int64  `json:"query_ms"`
	State   string `json:"state"`
	Type    string `json:"wait_type"`
	Event   string `json:"event"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c, e := pgx.Connect(ctx, os.Getenv("RADAR_DATABASE_URL"))
	if e != nil {
		fmt.Println("data connection failed")
		os.Exit(1)
	}
	defer c.Close(ctx)
	monitor, e := pgx.Connect(ctx, os.Getenv("RADAR_DATABASE_URL"))
	if e != nil {
		fmt.Println("monitor connection failed")
		os.Exit(1)
	}
	defer monitor.Close(ctx)
	mt, e := monitor.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if e != nil {
		panic(e)
	}
	defer mt.Rollback(ctx)
	tx, e := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		panic(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='60s'`); e != nil {
		panic(e)
	}
	var at time.Time
	var mem, blockSize string
	if e = tx.QueryRow(ctx, `SELECT date_trunc('minute',least(now(),max(to_time))),current_setting('work_mem'),current_setting('block_size') FROM coverage_ranges WHERE chain='solana'`).Scan(&at, &mem, &blockSize); e != nil {
		panic(e)
	}
	q, e := os.ReadFile("testdata/expect/evidence/h4-audit/sparse.sql")
	if e != nil {
		panic(e)
	}
	fmt.Printf("mode=REPEATABLE_READ_READ_ONLY as_of=%s work_mem=%s block_size=%s production_sql_unchanged=true\n", at.UTC().Format(time.RFC3339), mem, blockSize)
	start := time.Now()
	var samples []sample
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			before := time.Now()
			v := sample{AtMS: time.Since(start).Milliseconds()}
			if _, err := mt.Exec(ctx, `SELECT pg_stat_clear_snapshot()`); err != nil {
				fmt.Println("monitor clear snapshot failed")
				return
			}
			err := mt.QueryRow(ctx, `SELECT state,coalesce(wait_event_type,''),coalesce(wait_event,'') FROM pg_stat_activity WHERE pid=$1`, int32(c.PgConn().PID())).Scan(&v.State, &v.Type, &v.Event)
			if err != nil {
				fmt.Println("monitor sample failed")
				return
			}
			v.QueryMS = time.Since(before).Milliseconds()
			samples = append(samples, v)
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-stop:
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	rows, e := tx.Query(ctx, string(q), "solana", at.Add(-24*time.Hour), at, []int64{86400}, int64(1), int64(1), "24h")
	if e != nil {
		close(stop)
		wg.Wait()
		panic(e)
	}
	var count, bytes int64
	var first, last time.Duration
	for rows.Next() {
		if count == 0 {
			first = time.Since(start)
		}
		last = time.Since(start)
		count++
		for _, v := range rows.RawValues() {
			bytes += int64(len(v))
		}
	}
	err := rows.Err()
	rows.Close()
	elapsed := time.Since(start)
	close(stop)
	wg.Wait()
	if err != nil {
		panic(err)
	}
	raw, e := json.MarshalIndent(samples, "", "  ")
	if e != nil {
		panic(e)
	}
	if e = os.WriteFile("testdata/expect/evidence/h4-27-wait-samples.json", raw, 0600); e != nil {
		panic(e)
	}
	fmt.Printf("rows=%d field_value_bytes=%d first_row=%s last_row=%s elapsed=%s samples=%d\n", count, bytes, first, last, elapsed, len(samples))
	counts := map[string]int{}
	for _, v := range samples {
		counts[v.State+"/"+v.Type+"/"+v.Event]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("sample_state=%q count=%d\n", k, counts[k])
	}
	fmt.Println("Samples are observations, not an exact time decomposition; server execution and transport overlap. No cloud writes.")
}
