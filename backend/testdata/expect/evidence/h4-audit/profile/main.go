// This manual audit never changes the cloud database. Local writes are fenced
// to loopback and use a fresh database with the repository's formal migrations.
package main

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/meme-radar/radar/internal/store"
)

var columns = []string{"projection_version", "chain", "window", "token_address", "symbol", "name", "image_url", "launch_platform", "launch_at",
	"price_usd", "price_native", "market_cap_usd", "market_cap_native", "price_change_pct", "price_change_pct_native", "liquidity_usd", "liquidity_native",
	"trades", "buys", "sells", "observed_trades", "observed_buys", "observed_sells", "coverage", "volume_usd", "volume_native", "net_inflow_usd", "net_inflow_native",
	"sm_holders", "sm_trades", "holders", "age_seconds", "dexes", "socials", "sparkline", "sparkline_native", "dev_holding_pct"}

type observation struct {
	values              []any
	usd, native         []pgtype.Numeric
	buckets             []int32
	seedUSD, seedNative pgtype.Numeric
}

// Destinations and cached ScanPlans match collectInflowRows/baseRows.Scan:
// scalar destinations are *any, with typed Numeric arrays and nullable seeds.
func targets(v *observation) []any {
	v.values = make([]any, len(columns))
	dst := make([]any, len(columns)+3)
	for i := range v.values {
		dst[i] = &v.values[i]
	}
	dst[34], dst[35] = &v.usd, &v.native
	dst[37], dst[38], dst[39] = &v.buckets, &v.seedUSD, &v.seedNative
	return dst
}
func decode(raw [][][]byte, plans []pgtype.ScanPlan) []observation {
	out := make([]observation, len(raw))
	for n, row := range raw {
		dst := targets(&out[n])
		for i, p := range plans {
			if err := p.Scan(row[i], dst[i]); err != nil {
				panic(fmt.Errorf("decode column %d: %w", i, err))
			}
		}
		out[n].values[34], out[n].values[35] = out[n].usd, out[n].native
	}
	return out
}

// Identical exact carry operation to production; never numeric arithmetic.
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
func cpuNS() int64 {
	var v syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &v); err != nil {
		panic(err)
	}
	return (v.Utime.Sec+v.Stime.Sec)*1e9 + int64(v.Utime.Usec+v.Stime.Usec)*1000
}

var decodedSink []observation
var fillSink [][2][]pgtype.Numeric
var numericSink []any

func benchmark(name, dir string, fn func(), rows int) {
	f, err := os.Create(filepath.Join(dir, name+"-cpu.pprof"))
	if err != nil {
		panic(err)
	}
	if err = pprof.StartCPUProfile(f); err != nil {
		panic(err)
	}
	var cpu int64
	var iterations int
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		start := cpuNS()
		for n := 0; n < b.N; n++ {
			fn()
		}
		cpu = cpuNS() - start
		iterations = b.N
		b.StopTimer()
	})
	pprof.StopCPUProfile()
	if err = f.Close(); err != nil {
		panic(err)
	}
	f, err = os.Create(filepath.Join(dir, name+"-heap.pprof"))
	if err != nil {
		panic(err)
	}
	if err = pprof.WriteHeapProfile(f); err != nil {
		panic(err)
	}
	if err = f.Close(); err != nil {
		panic(err)
	}
	fmt.Printf("benchmark=%s rows_per_op=%d iterations=%d wall_ns_per_op=%d cpu_ns_per_op=%d bytes_per_op=%d allocs_per_op=%d\n", name, rows, iterations, result.NsPerOp(), cpu/int64(iterations), result.AllocedBytesPerOp(), result.AllocsPerOp())
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c, err := pgx.Connect(ctx, os.Getenv("RADAR_DATABASE_URL"))
	if err != nil {
		fmt.Println("cloud connect failed")
		os.Exit(1)
	}
	defer c.Close(ctx)
	tx, err := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		panic(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='40s'`); err != nil {
		panic(err)
	}
	var at time.Time
	if err = tx.QueryRow(ctx, `SELECT date_trunc('minute',least(now(),max(to_time))) FROM coverage_ranges WHERE chain='solana'`).Scan(&at); err != nil {
		panic(err)
	}
	sqlBytes, err := os.ReadFile("testdata/expect/evidence/h4-audit/sparse.sql")
	if err != nil {
		panic(err)
	}
	args := []any{"solana", at.Add(-24 * time.Hour), at, []int64{86400}, int64(1), int64(1), "24h"}
	fmt.Printf("profile mode=REPEATABLE_READ_READ_ONLY chain=solana window=24h as_of=%s production_sql_unchanged=true H3_H4_H5_not_deployed=true\n", at.UTC().Format(time.RFC3339))
	var plan []byte
	start := time.Now()
	if err = tx.QueryRow(ctx, "EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) "+string(sqlBytes), args...).Scan(&plan); err != nil {
		panic(err)
	}
	if err = os.WriteFile("testdata/expect/evidence/h4-26c-segment-plan.json", plan, 0600); err != nil {
		panic(err)
	}
	var tree []map[string]any
	if err = json.Unmarshal(plan, &tree); err != nil {
		panic(err)
	}
	fmt.Printf("server execution_ms=%v planning_ms=%v client_explain_elapsed=%s\n", tree[0]["Execution Time"], tree[0]["Planning Time"], time.Since(start))
	raw, fields := receive(ctx, tx, string(sqlBytes), args)
	dir := "../.local/h4-segment-profile"
	if err = os.MkdirAll(dir, 0755); err != nil {
		panic(err)
	}
	sample := observation{}
	dst := targets(&sample)
	plans := make([]pgtype.ScanPlan, len(fields))
	for n, f := range fields {
		plans[n] = c.TypeMap().PlanScan(f.DataTypeOID, f.Format, dst[n])
		if plans[n] == nil {
			panic(fmt.Sprintf("no plan for column %d", n))
		}
	}
	cache, err := os.Create(filepath.Join(dir, "raw.gob"))
	if err != nil {
		panic(err)
	}
	if err = gob.NewEncoder(cache).Encode(struct {
		At     time.Time
		Fields []pgconn.FieldDescription
		Raw    [][][]byte
	}{at, fields, raw}); err != nil {
		panic(err)
	}
	if err = cache.Close(); err != nil {
		panic(err)
	}
	decoded := decode(raw, plans)
	benchmark("full-row-decode", dir, func() { decodedSink = decode(raw, plans) }, len(raw))
	decodedSink = nil
	runtime.GC()
	// Codec-only Numeric benchmark isolates exact numeric work; it excludes row
	// containers and nonnumeric fields, so it is a subset rather than an additive
	// stage of the full-row decoding benchmark above.
	var numericCols []int
	for n, f := range fields {
		if f.DataTypeOID == pgtype.NumericOID || f.DataTypeOID == pgtype.NumericArrayOID {
			numericCols = append(numericCols, n)
		}
	}
	numericSink = make([]any, len(fields))
	var arrUSD, arrNative []pgtype.Numeric
	var seedUSD, seedNative pgtype.Numeric
	numericDst := make([]any, len(fields))
	for n := range numericDst {
		numericDst[n] = &numericSink[n]
	}
	numericDst[34], numericDst[35], numericDst[38], numericDst[39] = &arrUSD, &arrNative, &seedUSD, &seedNative
	benchmark("numeric-codecs-only", dir, func() {
		for _, row := range raw {
			for _, n := range numericCols {
				if e := plans[n].Scan(row[n], numericDst[n]); e != nil {
					panic(e)
				}
			}
		}
	}, len(raw))
	benchmark("exact-20-bucket-carry", dir, func() {
		out := make([][2][]pgtype.Numeric, len(decoded))
		for n, v := range decoded {
			out[n][0], out[n][1] = fillInflowBuckets(v.seedUSD, v.seedNative, v.buckets, v.usd, v.native)
		}
		fillSink = out
	}, len(raw))
	fillSink = nil
	runtime.GC()
	values := make([][]any, len(decoded))
	for n, v := range decoded {
		v.values[34], v.values[35] = fillInflowBuckets(v.seedUSD, v.seedNative, v.buckets, v.usd, v.native)
		values[n] = v.values
	}
	localWrite(ctx, values, at)
	fmt.Println("PASS segment audit; no cloud writes and no production SQL/config/semantics changes")
}

func receive(ctx context.Context, tx pgx.Tx, sql string, args []any) ([][][]byte, []pgconn.FieldDescription) {
	start := time.Now()
	cpuStart := cpuNS()
	var memStart, memEnd runtime.MemStats
	runtime.ReadMemStats(&memStart)
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		panic(err)
	}
	fields := append([]pgconn.FieldDescription(nil), rows.FieldDescriptions()...)
	var raw [][][]byte
	var first, last time.Duration
	var bytes int64
	byColumn := make([]int64, len(fields))
	for rows.Next() {
		at := time.Since(start)
		if len(raw) == 0 {
			first = at
		}
		last = at
		vals := rows.RawValues()
		copied := make([][]byte, len(vals))
		for n, v := range vals {
			bytes += int64(len(v))
			byColumn[n] += int64(len(v))
			if v != nil {
				copied[n] = append([]byte{}, v...)
			}
		}
		raw = append(raw, copied)
	}
	if err = rows.Err(); err != nil {
		panic(err)
	}
	rows.Close()
	elapsed := time.Since(start)
	cpu := cpuNS() - cpuStart
	runtime.ReadMemStats(&memEnd)
	fmt.Printf("transfer rows=%d columns=%d field_value_bytes=%d data_row_protocol_bytes=%d first_row=%s last_row=%s elapsed=%s raw_copy_cpu_ns=%d raw_copy_alloc_bytes=%d raw_copy_allocs=%d\n", len(raw), len(fields), bytes, bytes+int64(len(raw)*(7+4*len(fields))), first, last, elapsed, cpu, memEnd.TotalAlloc-memStart.TotalAlloc, memEnd.Mallocs-memStart.Mallocs)
	fmt.Println("transfer bytes count exact RawValues payload plus DataRow headers; excludes TLS/TCP framing, RowDescription and completion messages; CPU includes buffering/copying, not Numeric decode")
	for n, f := range fields {
		fmt.Printf("column=%d name=%s oid=%d format=%d value_bytes=%d\n", n, f.Name, f.DataTypeOID, f.Format, byColumn[n])
	}
	return raw, fields
}

func localWrite(ctx context.Context, values [][]any, asOf time.Time) {
	cfg, err := pgx.ParseConfig(os.Getenv("RADAR_TEST_DATABASE_URL"))
	if err != nil {
		panic("local database configuration invalid")
	}
	if cfg.Host != "127.0.0.1" && cfg.Host != "localhost" {
		panic("local write benchmark requires loopback database")
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		panic("local admin connection failed")
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("h4_segment_%d_%d", os.Getpid(), time.Now().UnixNano())
	ident := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		panic(err)
	}
	defer func() {
		if _, e := admin.Exec(context.Background(), "DROP DATABASE "+ident+" WITH (FORCE)"); e != nil {
			fmt.Println("local temporary database cleanup failed")
		}
	}()
	cfg.Database = name
	localURL, err := url.Parse(cfg.ConnString())
	if err != nil || (localURL.Scheme != "postgres" && localURL.Scheme != "postgresql") {
		panic("local audit requires a postgres URL")
	}
	localURL.Path = "/" + name
	st, err := store.Open(ctx, localURL.String())
	if err != nil {
		panic("local store open failed")
	}
	defer st.Close()
	if err = st.Migrate(ctx); err != nil {
		panic(err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		panic("local writer open failed")
	}
	defer conn.Close(ctx)
	for round := int64(1); round <= 3; round++ {
		tx, err := conn.Begin(ctx)
		if err != nil {
			panic(err)
		}
		start := time.Now()
		var version int64
		if err = tx.QueryRow(ctx, `SELECT nextval('projection_version_seq')`).Scan(&version); err != nil {
			panic(err)
		}
		for _, v := range values {
			v[0] = version
		}
		beforeCopy := time.Now()
		count, err := tx.CopyFrom(ctx, pgx.Identifier{"inflow_rows"}, columns, pgx.CopyFromRows(values))
		if err != nil {
			panic(err)
		}
		copyTime := time.Since(beforeCopy)
		switchStart := time.Now()
		var active int64
		if err = tx.QueryRow(ctx, `SELECT version FROM sm_lists WHERE status='active' FOR SHARE`).Scan(&active); err != nil {
			panic(err)
		}
		if active != 1 {
			panic("unexpected local active version")
		}
		if round > 1 {
			if _, err = tx.Exec(ctx, `UPDATE inflow_rows SET retired_at=$4 WHERE projection_version=$1 AND chain=$2 AND "window"=$3`, version-1, "solana", "24h", time.Now()); err != nil {
				panic(err)
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO projection_heads(name,chain,"window",projection_version,as_of,list_version,updated_at) VALUES('inflow','solana','24h',$1,$2,1,now()) ON CONFLICT(name,chain,"window") DO UPDATE SET projection_version=excluded.projection_version,as_of=excluded.as_of,updated_at=excluded.updated_at`, version, asOf); err != nil {
			panic(err)
		}
		if err = tx.Commit(ctx); err != nil {
			panic(err)
		}
		fmt.Printf("local_same_cloud_rows round=%d rows=%d copy=%s retire_head_commit=%s full_transaction=%s cloud_write=NOT_RUN\n", round, count, copyTime, time.Since(switchStart), time.Since(start))
	}
}
