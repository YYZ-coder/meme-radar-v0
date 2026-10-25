package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
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

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cloud, err := pgx.Connect(ctx, os.Getenv("RADAR_DATABASE_URL"))
	if err != nil {
		panic("cloud readonly connection failed")
	}
	defer cloud.Close(ctx)
	snapshot, err := cloud.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		panic(err)
	}
	defer snapshot.Rollback(ctx)
	if _, err = snapshot.Exec(ctx, `SET LOCAL statement_timeout='60s'`); err != nil {
		panic(err)
	}
	var cache struct {
		At     time.Time
		Fields []pgconn.FieldDescription
		Raw    [][][]byte
	}
	if err = snapshot.QueryRow(ctx, `SELECT date_trunc('minute',least(now(),max(to_time))) FROM coverage_ranges WHERE chain='solana'`).Scan(&cache.At); err != nil {
		panic(err)
	}
	source, err := os.ReadFile("testdata/expect/evidence/h4-audit/sparse.sql")
	if err != nil {
		panic(err)
	}
	rows, err := snapshot.Query(ctx, string(source), "solana", cache.At.Add(-24*time.Hour), cache.At, []int64{86400}, int64(1), int64(1), "24h")
	if err != nil {
		panic(err)
	}
	cache.Fields = append([]pgconn.FieldDescription(nil), rows.FieldDescriptions()...)
	for rows.Next() {
		raw := rows.RawValues()
		copied := make([][]byte, len(raw))
		for i, v := range raw {
			if v != nil {
				copied[i] = append([]byte{}, v...)
			}
		}
		cache.Raw = append(cache.Raw, copied)
	}
	if err = rows.Err(); err != nil {
		panic(err)
	}
	rows.Close()
	tm := pgtype.NewMap()
	sample := observation{}
	dst := targets(&sample)
	plans := make([]pgtype.ScanPlan, len(cache.Fields))
	for i, fd := range cache.Fields {
		plans[i] = tm.PlanScan(fd.DataTypeOID, fd.Format, dst[i])
		if plans[i] == nil {
			panic(fmt.Sprintf("no scan plan column %d", i))
		}
	}
	decoded := decode(cache.Raw, plans)
	input := make([][]any, len(decoded))
	for i, v := range decoded {
		input[i] = append(v.values, v.buckets, v.seedUSD, v.seedNative)
	}
	cfg, err := pgx.ParseConfig(os.Getenv("RADAR_TEST_DATABASE_URL"))
	if err != nil {
		panic("bad local config")
	}
	if cfg.Host != "localhost" && cfg.Host != "127.0.0.1" {
		panic("loopback required")
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		panic("local admin unavailable")
	}
	defer admin.Close(ctx)
	db := fmt.Sprintf("h4_array_%d_%d", os.Getpid(), time.Now().UnixNano())
	ident := pgx.Identifier{db}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		panic(err)
	}
	defer func() {
		if _, e := admin.Exec(context.Background(), "DROP DATABASE "+ident+" WITH(FORCE)"); e != nil {
			fmt.Println("cleanup failed")
		}
	}()
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		panic(err)
	}
	u.Path = "/" + db
	st, err := store.Open(ctx, u.String())
	if err != nil {
		panic("local store unavailable")
	}
	defer st.Close()
	if err = st.Migrate(ctx); err != nil {
		panic(err)
	}
	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		panic("local writer unavailable")
	}
	defer conn.Close(ctx)
	names, defs := make([]string, 40), make([]string, 40)
	types := map[uint32]string{20: "bigint", 25: "text", 17: "bytea", 1184: "timestamptz", 1700: "numeric", 1009: "text[]", 3802: "jsonb", 1231: "numeric[]", 1007: "int[]"}
	for i, fd := range cache.Fields {
		names[i] = fmt.Sprintf("c%d", i)
		typ := types[fd.DataTypeOID]
		if i == 1 {
			typ = "chain_id"
		}
		if typ == "" {
			panic(fmt.Sprintf("unknown column type %d", i))
		}
		defs[i] = names[i] + " " + typ
	}
	if _, err = conn.Exec(ctx, "CREATE TEMP TABLE h4_actual_input("+strings.Join(defs, ",")+")"); err != nil {
		panic(err)
	}
	if _, err = conn.CopyFrom(ctx, pgx.Identifier{"h4_actual_input"}, names, pgx.CopyFromRows(input)); err != nil {
		panic(err)
	}
	sql, err := os.ReadFile("testdata/expect/evidence/h4-audit/local-array.sql")
	if err != nil {
		panic(err)
	}
	quoted := make([]string, len(columns))
	for i, c := range columns {
		quoted[i] = pgx.Identifier{c}.Sanitize()
	}
	fmt.Printf("local_same_actual_rows=%d as_of=%s exact_live_readonly_cloud_input=true shared_production_array_expression=true staging_load_excluded=true\n", len(input), cache.At.UTC().Format(time.RFC3339))
	for round := int64(1); round <= 3; round++ {
		tx, e := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if e != nil {
			panic(e)
		}
		start := time.Now()
		var version int64
		if e = tx.QueryRow(ctx, `SELECT nextval('projection_version_seq')`).Scan(&version); e != nil {
			panic(e)
		}
		insertStart := time.Now()
		tag, e := tx.Exec(ctx, "INSERT INTO inflow_rows("+strings.Join(quoted, ",")+") "+string(sql), version)
		if e != nil {
			panic(e)
		}
		insertElapsed := time.Since(insertStart)
		headStart := time.Now()
		var active int64
		if e = tx.QueryRow(ctx, `SELECT version FROM sm_lists WHERE status='active' FOR SHARE`).Scan(&active); e != nil {
			panic(e)
		}
		if active != 1 {
			panic("unexpected active")
		}
		if round > 1 {
			if _, e = tx.Exec(ctx, `UPDATE inflow_rows SET retired_at=$4 WHERE projection_version=$1 AND chain=$2 AND "window"=$3`, version-1, "solana", "24h", time.Now()); e != nil {
				panic(e)
			}
		}
		if _, e = tx.Exec(ctx, `INSERT INTO projection_heads(name,chain,"window",projection_version,as_of,list_version,updated_at,rebuild_pending) VALUES('inflow','solana','24h',$1,$2,1,now(),false) ON CONFLICT(name,chain,"window") DO UPDATE SET projection_version=excluded.projection_version,as_of=excluded.as_of,list_version=excluded.list_version,updated_at=excluded.updated_at,rebuild_pending=false`, version, cache.At); e != nil {
			panic(e)
		}
		if e = tx.Commit(ctx); e != nil {
			panic(e)
		}
		fmt.Printf("round=%d rows=%d insert_select_fill_write=%s fence_retire_head_commit=%s full_transaction=%s cloud_write=NOT_RUN\n", round, tag.RowsAffected(), insertElapsed, time.Since(headStart), time.Since(start))
	}
}
