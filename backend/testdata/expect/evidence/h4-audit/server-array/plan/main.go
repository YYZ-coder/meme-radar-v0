package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
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
	e = tx.QueryRow(ctx, `SELECT date_trunc('minute',least(now(),max(to_time))) FROM coverage_ranges WHERE chain='solana'`).Scan(&at)
	if e != nil {
		panic(e)
	}
	fmt.Printf("server_array_actual_preview chain=solana window=24h as_of=%s mode=REPEATABLE_READ_READ_ONLY H3_H4_H5_not_deployed=true\n", at.UTC().Format(time.RFC3339))
	q, e := os.ReadFile("testdata/expect/evidence/h4-audit/array.sql")
	if e != nil {
		panic(e)
	}
	var raw []byte
	var plans []map[string]any
	for round := 1; round <= 2; round++ {
		start := time.Now()
		e = tx.QueryRow(ctx, "EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) "+string(q), "solana", at.Add(-24*time.Hour), at, []int64{86400}, int64(1), int64(1), "24h").Scan(&raw)
		if e != nil {
			panic(e)
		}
		if e = json.Unmarshal(raw, &plans); e != nil {
			panic(e)
		}
		p := plans[0]["Plan"].(map[string]any)
		fmt.Printf("round=%d natural_cache_first_then_warm=true rows=%v server_execution_ms=%v planning_ms=%v client_explain_elapsed=%s shared_hit=%v shared_read=%v temp_read=%v temp_written=%v\n", round, p["Actual Rows"], plans[0]["Execution Time"], plans[0]["Planning Time"], time.Since(start), p["Shared Hit Blocks"], p["Shared Read Blocks"], p["Temp Read Blocks"], p["Temp Written Blocks"])
		if e = os.WriteFile(fmt.Sprintf("testdata/expect/evidence/h4-32-array-plan-%d.json", round), raw, 0600); e != nil {
			panic(e)
		}
	}
	var walk func(map[string]any, int)
	walk = func(p map[string]any, d int) {
		if d < 7 || p["Index Name"] != nil || p["CTE Name"] != nil {
			fmt.Printf("depth=%d node=%v relation=%v cte=%v index=%v actual_rows=%v loops=%v total_ms=%v removed_join=%v filter=%v index_cond=%v\n", d, p["Node Type"], p["Relation Name"], p["CTE Name"], p["Index Name"], p["Actual Rows"], p["Actual Loops"], p["Actual Total Time"], p["Rows Removed by Join Filter"], p["Join Filter"], p["Index Cond"])
		}
		children, _ := p["Plans"].([]any)
		for _, sub := range children {
			walk(sub.(map[string]any), d+1)
		}
	}
	walk(plans[0]["Plan"].(map[string]any), 0)
	fmt.Printf("root_rows=%v root_total_ms=%v\n", plans[0]["Plan"].(map[string]any)["Actual Rows"], plans[0]["Plan"].(map[string]any)["Actual Total Time"])
	rows, e := tx.Query(ctx, `SELECT indexname,indexdef FROM pg_indexes WHERE tablename='candles' ORDER BY indexname`)
	if e != nil {
		panic(e)
	}
	defer rows.Close()
	for rows.Next() {
		var name, def string
		if e = rows.Scan(&name, &def); e != nil {
			panic(e)
		}
		fmt.Printf("candle_index name=%s definition=%s\n", name, def)
	}
	if rows.Err() != nil {
		panic(rows.Err())
	}
}
