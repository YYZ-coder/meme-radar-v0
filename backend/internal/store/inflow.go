package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const insertSmTradesSQL = `INSERT INTO sm_trades(version,block_time,chain,block_num,tx_index,ix_index,token_address,wallet,side,value_usd,value_native,is_large)
 SELECT m.version,t.block_time,t.chain,t.block_num,t.tx_index,t.ix_index,t.token_address,t.wallet,t.side,t.value_usd,t.value_native,
 CASE WHEN t.side='buy' THEN t.value_usd>5000 END
 FROM trades t JOIN sm_list_members m ON m.chain=t.chain AND m.address=t.wallet AND NOT m.excluded
 JOIN sm_lists l ON l.version=m.version AND (l.status='active' OR (l.status='building' AND l.prepared_at IS NOT NULL AND l.build_status IN ('running','ready')))
 WHERE t.chain=$1 AND t.block_time >= $2::timestamptz-interval '48 hours'
  AND t.attribution IN ('confirmed','inferred') AND t.wallet IS NOT NULL`

// RefreshSmTrades copies eligible active and building member trades for chain.
func (s *Store) RefreshSmTrades(ctx context.Context, chain string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('h4:smtrades:'||$1,0))`, chain); err != nil {
		return err
	}
	var blocks []int64
	var mark *time.Time
	err = tx.QueryRow(ctx, `SELECT coalesce(array_agg(block_num),'{}'),max(parsed_at) FROM chain_blocks
 WHERE chain=$1 AND parsed_at>coalesce((SELECT at FROM compute_marks WHERE chain=$1 AND name='smtrades'),'-infinity'::timestamptz)
 AND block_time >= $2::timestamptz-interval '48 hours'`, chain, now).Scan(&blocks, &mark)
	if err != nil {
		return fmt.Errorf("store: smart-money parse mark: %w", err)
	}
	if len(blocks) > 0 {
		if _, err = tx.Exec(ctx, `DELETE FROM sm_trades WHERE chain=$1 AND block_num=ANY($2::bigint[])`, chain, blocks); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, insertSmTradesSQL+` AND t.block_num=ANY($3::bigint[]) ON CONFLICT DO NOTHING`, chain, now, blocks); err != nil {
			return fmt.Errorf("store: copy smart-money trades: %w", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO compute_marks(chain,name,at) VALUES($1,'smtrades',$2)
   ON CONFLICT(chain,name) DO UPDATE SET at=excluded.at`, chain, mark); err != nil {
			return err
		}
	}
	rows, err := tx.Query(ctx, `SELECT minute FROM smtrades_dirty WHERE chain=$1 FOR UPDATE`, chain)
	if err != nil {
		return err
	}
	var minutes []time.Time
	for rows.Next() {
		var m time.Time
		if err = rows.Scan(&m); err != nil {
			rows.Close()
			return err
		}
		minutes = append(minutes, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(minutes) > 0 {
		_, err = tx.Exec(ctx, `UPDATE sm_trades st SET value_usd=t.value_usd,value_native=t.value_native,
   is_large=CASE WHEN t.side='buy' THEN t.value_usd>5000 END FROM trades t
   WHERE st.chain=$1 AND t.chain=st.chain AND t.block_time=st.block_time AND t.block_num=st.block_num
   AND t.tx_index=st.tx_index AND t.ix_index=st.ix_index
   AND st.block_time>=ANY($2::timestamptz[]) AND date_trunc('minute',st.block_time)=ANY($2::timestamptz[])`, chain, minutes)
		if err != nil {
			return fmt.Errorf("store: reprice smart-money trades: %w", err)
		}
		if _, err = tx.Exec(ctx, `DELETE FROM smtrades_dirty WHERE chain=$1 AND minute=ANY($2::timestamptz[])`, chain, minutes); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sm_trades st USING sm_lists l WHERE st.chain=$1 AND st.version=l.version AND (
  st.block_time<$2::timestamptz-interval '48 hours' OR (l.status='retired' AND EXISTS(
   SELECT 1 FROM sm_lists child WHERE child.parent_version=l.version AND child.activated_at<=$2::timestamptz-interval '30 minutes'
  )) OR (l.status='building' AND l.build_status='failed' AND
   (SELECT max(j.updated_at) FROM jobs j WHERE j.kind='list_build' AND (j.payload->>'version')::bigint=l.version AND j.status='failed')
   <=$2::timestamptz-interval '30 minutes'))`, chain, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PrepareListBuild persists a fenced, reentrant building snapshot and its trade
// backfill before activation. Readers continue using the current active list.
func (s *Store) PrepareListBuild(ctx context.Context, j ListJob, members []ListMember, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owned bool
	err = tx.QueryRow(ctx, `SELECT j.status='running' AND j.attempts=$2 AND j.lease_until>$3 AND l.status='building'
  AND l.build_status<>'failed' AND l.parent_version=(SELECT version FROM sm_lists WHERE status='active')
  FROM jobs j JOIN sm_lists l ON l.version=$4 WHERE j.id=$1 FOR UPDATE OF j,l`, j.ID, j.Attempts, now, j.Version).Scan(&owned)
	if err != nil {
		return err
	}
	if !owned {
		return ErrJobLeaseLost
	}
	// The same chain locks also fence the incremental copy against replacement.
	for _, chain := range []string{"base", "bsc", "solana"} {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('h4:smtrades:'||$1,0))`, chain); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sm_list_members WHERE version=$1`, j.Version); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sm_trades WHERE version=$1`, j.Version); err != nil {
		return err
	}
	if err = copyListMembers(ctx, tx, j.Version, members); err != nil {
		return err
	}
	if err = onBuild(ctx, tx, j.Version, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sm_lists SET prepared_at=$2,build_status='ready' WHERE version=$1`, j.Version, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// onBuild runs in the separate fenced preparation transaction, never activation.
func onBuild(ctx context.Context, tx pgx.Tx, version int64, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO sm_trades(version,block_time,chain,block_num,tx_index,ix_index,token_address,wallet,side,value_usd,value_native,is_large)
 SELECT m.version,t.block_time,t.chain,t.block_num,t.tx_index,t.ix_index,t.token_address,t.wallet,t.side,t.value_usd,t.value_native,
 CASE WHEN t.side='buy' THEN t.value_usd>5000 END
 FROM trades t JOIN sm_list_members m ON m.chain=t.chain AND m.address=t.wallet AND NOT m.excluded
 WHERE m.version=$1 AND t.block_time>=$2::timestamptz-interval '48 hours' AND t.block_time<$2
 AND t.attribution IN ('confirmed','inferred') AND t.wallet IS NOT NULL ON CONFLICT DO NOTHING`, version, now)
	if err != nil {
		return fmt.Errorf("store: prepare smart-money trades: %w", err)
	}
	return nil
}

// Both preparation and the legacy direct activation path write identical rows.
func copyListMembers(ctx context.Context, tx pgx.Tx, version int64, members []ListMember) error {
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"sm_list_members"}, []string{"version", "chain", "address", "label", "tags", "twitter", "sources", "added_at", "excluded"}, pgx.CopyFromSlice(len(members), func(i int) ([]any, error) {
		m := members[i]
		return []any{version, m.Chain, m.Address, m.Label, m.Tags, m.Twitter, m.Sources, m.AddedAt, m.Excluded}, nil
	}))
	return err
}

// onActivate only switches list identity and requests a full rebuild. The heavy
// trade backfill has already committed in PrepareListBuild's own transaction.
func onActivate(ctx context.Context, tx pgx.Tx, version int64) error {
	_, err := tx.Exec(ctx, `UPDATE projection_heads SET list_version=$1,rebuild_pending=true WHERE name='inflow'`, version)
	return err
}

// OverviewValues is the versioned, coverage-aware smart-money overview.
type OverviewValues struct {
	Version                                 int64
	Complete                                bool
	ActiveWallets, TrackedTokens, NewTokens *int64
	Large, Small, LargeShare, SmallShare    *string
	Net, Buy, Sell, Change, BuyShare        *string
	Bars                                    []*string
}

// InflowOverview measures one version's exact half-open window in a single
// statement snapshot; coverage and unknown prices are independent dimensions.
func (s *Store) InflowOverview(ctx context.Context, chain string, window time.Duration, asOf time.Time) (OverviewValues, error) {
	var out OverviewValues
	var wallets, tracked, fresh int64
	var previousComplete bool
	err := s.pool.QueryRow(ctx, `WITH selected AS (
  SELECT unnest(CASE WHEN $1='' THEN ARRAY['base','bsc','solana'] ELSE ARRAY[$1] END) AS chain
 ), active AS(SELECT version FROM sm_lists WHERE status='active'), current_trades AS MATERIALIZED (
  SELECT st.* FROM sm_trades st JOIN active USING(version) WHERE ($1='' OR st.chain::text=$1)
   AND st.block_time>=$2::timestamptz-$3*interval '1 second' AND st.block_time<$2
 ), amounts AS (
  SELECT CASE WHEN count(*) FILTER(WHERE side='buy' AND value_usd IS NULL)=0
   THEN coalesce(sum(value_usd) FILTER(WHERE side='buy'),0) END AS buy,
   CASE WHEN count(*) FILTER(WHERE side='sell' AND value_usd IS NULL)=0
   THEN coalesce(sum(value_usd) FILTER(WHERE side='sell'),0) END AS sell,
   CASE WHEN count(*) FILTER(WHERE side='buy' AND (value_usd IS NULL OR is_large IS NULL))=0
   THEN coalesce(sum(value_usd) FILTER(WHERE side='buy' AND is_large),0) END AS large,
   CASE WHEN count(*) FILTER(WHERE side='buy' AND (value_usd IS NULL OR is_large IS NULL))=0
   THEN coalesce(sum(value_usd) FILTER(WHERE side='buy' AND NOT is_large),0) END AS small FROM current_trades
 ), previous AS (
  SELECT CASE WHEN count(*) FILTER(WHERE st.value_usd IS NULL)=0
   THEN coalesce(sum(CASE WHEN st.side='buy' THEN st.value_usd ELSE -st.value_usd END),0) END AS net
  FROM sm_trades st JOIN active USING(version) WHERE ($1='' OR st.chain::text=$1)
   AND st.block_time>=$2::timestamptz-2*$3*interval '1 second' AND st.block_time<$2::timestamptz-$3*interval '1 second'
 ), bars AS (
  SELECT bucket,CASE WHEN count(*) FILTER(WHERE st.block_time IS NOT NULL AND st.value_usd IS NULL)=0
   THEN coalesce(sum(CASE WHEN st.side='buy' THEN st.value_usd ELSE -st.value_usd END),0) END AS net
  FROM generate_series(0,15) bucket LEFT JOIN current_trades st ON
   floor(extract(epoch FROM(st.block_time-($2::timestamptz-$3*interval '1 second')))*16/$3)=bucket
  GROUP BY bucket
 )
 SELECT active.version,
 NOT EXISTS(SELECT 1 FROM selected s WHERE NOT EXISTS(SELECT 1 FROM coverage_ranges c
  WHERE c.chain::text=s.chain AND c.from_time<=$2::timestamptz-$3*interval '1 second' AND c.to_time>=$2)),
 NOT EXISTS(SELECT 1 FROM selected s WHERE NOT EXISTS(SELECT 1 FROM coverage_ranges c
  WHERE c.chain::text=s.chain AND c.from_time<=$2::timestamptz-2*$3*interval '1 second' AND c.to_time>=$2::timestamptz-$3*interval '1 second')),
 (SELECT count(DISTINCT (st.chain,st.wallet)) FROM current_trades st),
 (SELECT count(*) FROM inflow_rows r JOIN projection_heads h ON h.projection_version=r.projection_version
  AND h.chain=r.chain AND h."window"=r."window" WHERE h.name='inflow' AND h."window"=$4 AND ($1='' OR h.chain::text=$1)),
 (SELECT count(*) FROM token_info t WHERE ($1='' OR t.chain::text=$1)
  AND t.launch_at>=$2::timestamptz-$3*interval '1 second' AND t.launch_at<$2),
 trim_scale(large)::text,trim_scale(small)::text,
 trim_scale(round(round(large*100,scale(large*100)+40)/nullif(large+small,0),20))::text,
 trim_scale(round(round(small*100,scale(small*100)+40)/nullif(large+small,0),20))::text,
 trim_scale(buy-sell)::text,trim_scale(buy)::text,trim_scale(sell)::text,
 trim_scale(round(round((buy-sell-previous.net)*100,scale((buy-sell-previous.net)*100)+40)/nullif(abs(previous.net),0),20))::text,
 trim_scale(round(round(buy*100,scale(buy*100)+40)/nullif(buy+sell,0),20))::text,
 (SELECT array_agg(trim_scale(net)::text ORDER BY bucket) FROM bars)
 FROM active CROSS JOIN amounts CROSS JOIN previous`, chain, asOf, int64(window/time.Second), inflowWindowName(window)).Scan(
		&out.Version, &out.Complete, &previousComplete, &wallets, &tracked, &fresh, &out.Large, &out.Small, &out.LargeShare, &out.SmallShare,
		&out.Net, &out.Buy, &out.Sell, &out.Change, &out.BuyShare, &out.Bars)
	if err != nil {
		return out, fmt.Errorf("store: inflow overview: %w", err)
	}
	if out.Complete {
		out.ActiveWallets = &wallets
		out.TrackedTokens = &tracked
		out.NewTokens = &fresh
	} else {
		out.Large, out.Small, out.LargeShare, out.SmallShare = nil, nil, nil, nil
		out.Net, out.Buy, out.Sell, out.Change, out.BuyShare = nil, nil, nil, nil, nil
		out.Bars = make([]*string, 16)
	}
	if !previousComplete {
		out.Change = nil
	}
	return out, nil
}

func inflowWindowName(w time.Duration) string {
	return map[time.Duration]string{time.Minute: "1m", 5 * time.Minute: "5m", time.Hour: "1h", 4 * time.Hour: "4h", 24 * time.Hour: "24h"}[w]
}

// ProjectionBuild describes a published inflow version, or an unchanged head.
// QueryTime includes the INSERT SELECT with exact bucket filling; WriteTime is
// the activation fence, old-version retirement, head switch and commit. FillTime
// is zero because no monetary observations are decoded or filled in Go.
type ProjectionBuild struct {
	Version                        int64
	AsOf                           time.Time
	Rows                           int64
	Rebuilt                        bool
	SkipReason                     string
	QueryTime, FillTime, WriteTime time.Duration
}

// RebuildInflow atomically publishes a complete list for one chain and window.
func (s *Store) RebuildInflow(ctx context.Context, chain, window string, now time.Time) (ProjectionBuild, error) {
	var out ProjectionBuild
	width, err := time.ParseDuration(window)
	if err != nil || inflowWindowName(width) == "" {
		return out, fmt.Errorf("store: unsupported inflow window %q", window)
	}
	asOf, ok, err := s.ChainAsOf(ctx, chain, now)
	if err != nil {
		return out, err
	}
	if !ok {
		asOf = now.UTC().Truncate(time.Minute)
	}
	out.AsOf = asOf
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	// Keep sort/hash spill tuning local to this single-writer transaction.
	if _, err = tx.Exec(ctx, "SET LOCAL work_mem='64MB'"); err != nil {
		return out, fmt.Errorf("store: inflow work_mem: %w", err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM inflow_rows r WHERE retired_at<=$1::timestamptz-interval '15 minutes'
  AND NOT EXISTS(SELECT 1 FROM projection_heads h WHERE h.projection_version=r.projection_version AND h.chain=r.chain AND h."window"=r."window")`, now); err != nil {
		return out, err
	}
	var list int64
	if err = tx.QueryRow(ctx, `SELECT version FROM sm_lists WHERE status='active'`).Scan(&list); err != nil {
		return out, err
	}
	var oldVersion, headList int64
	var headAt time.Time
	var pending bool
	err = tx.QueryRow(ctx, `SELECT projection_version,as_of,list_version,rebuild_pending FROM projection_heads WHERE name='inflow' AND chain=$1 AND "window"=$2`, chain, window).Scan(&oldVersion, &headAt, &headList, &pending)
	if err != nil && err != pgx.ErrNoRows {
		return out, err
	}
	if err == nil && headAt.Equal(asOf) && headList == list && !pending {
		out.Version = oldVersion
		return out, tx.Commit(ctx)
	}
	out.Version = oldVersion
	reason, err := projectionReadiness(ctx, tx, chain, asOf.Add(-width), asOf)
	if err != nil {
		return out, err
	}
	if reason != "" {
		out.SkipReason = reason
		return out, tx.Commit(ctx)
	}
	if err = tx.QueryRow(ctx, `SELECT nextval('projection_version_seq')`).Scan(&out.Version); err != nil {
		return out, err
	}
	seconds := []int64{int64(width / time.Second)}
	started := time.Now()
	quoted := make([]string, len(inflowColumns))
	for i, name := range inflowColumns {
		quoted[i] = pgx.Identifier{name}.Sanitize()
	}
	query := "INSERT INTO inflow_rows (" + strings.Join(quoted, ",") + ") " + inflowPublishSelectSQL(inflowSelectSQL(width >= time.Hour), width >= time.Hour)
	tag, err := tx.Exec(ctx, query, chain, asOf.Add(-width), asOf, seconds, out.Version, list, window)
	out.QueryTime = time.Since(started)
	if err != nil {
		return out, fmt.Errorf("store: insert inflow: %w", err)
	}
	out.Rows = tag.RowsAffected()
	started = time.Now()
	// Acquire the short activation fence only after the expensive read/build.
	// A list changed since this repeatable-read snapshot aborts publication.
	var active int64
	if err = tx.QueryRow(ctx, `SELECT version FROM sm_lists WHERE status='active' FOR SHARE`).Scan(&active); err != nil {
		return out, err
	}
	if active != list {
		return out, ErrJobLeaseLost
	}
	if oldVersion != 0 {
		if _, err = tx.Exec(ctx, `UPDATE inflow_rows SET retired_at=$4 WHERE projection_version=$1 AND chain=$2 AND "window"=$3`, oldVersion, chain, window, now); err != nil {
			return out, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO projection_heads(name,chain,"window",projection_version,as_of,list_version,updated_at,rebuild_pending)
 VALUES('inflow',$1,$2,$3,$4,$5,$6,false) ON CONFLICT(name,chain,"window") DO UPDATE SET
 projection_version=excluded.projection_version,as_of=excluded.as_of,list_version=excluded.list_version,updated_at=excluded.updated_at,rebuild_pending=false`, chain, window, out.Version, asOf, list, now)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	out.WriteTime = time.Since(started)
	out.Rebuilt = true
	return out, nil
}

// ChainWindowValues is H1's observed window for one token in a chain batch.
type ChainWindowValues struct {
	Address []byte
	WindowValues
}

func chainWindowsSQL(native bool) string {
	return chainWindowsCTEs(true) + " " + chainWindowsSelect(native, true)
}

func chainWindowsCTEs(wallets bool) string {
	ctes := `WITH windows AS MATERIALIZED (
  SELECT w.*,` + windowCoverageSQL + ` AS complete FROM(
   SELECT seconds,n,$3::timestamptz-seconds*interval '1 second' AS from_time
   FROM unnest($4::bigint[]) WITH ORDINALITY w(seconds,n)
  )w
 ), stats AS MATERIALIZED (
  SELECT ps.token_address,w.seconds,` + windowCountsSQL + ", " + fmt.Sprintf(windowAmountsSQL, "usd") + ", " + fmt.Sprintf(windowAmountsSQL, "native") + `,
   array_agg(DISTINCT ps.dex ORDER BY ps.dex) AS dexes
  FROM windows w JOIN pool_minute_stats ps ON ps.chain=$1 AND ps.minute>=w.from_time AND ps.minute<$3
  GROUP BY ps.token_address,w.seconds
 )`
	source := "stats"
	if wallets {
		ctes += `, wallet_counts AS MATERIALIZED (
   SELECT t.token_address,w.seconds,count(DISTINCT t.wallet) AS traders
   FROM windows w JOIN trades t ON t.chain=$1 AND t.block_time>=w.from_time AND t.block_time<$3
   GROUP BY t.token_address,w.seconds
  )`
		source = "wallet_counts"
	}
	return ctes + `, tokens AS MATERIALIZED (SELECT DISTINCT token_address FROM ` + source + ` WHERE $2::timestamptz IS NOT NULL)`
}

func chainWindowsSelect(native, wallets bool) string {
	unit, price := "usd", "t.price_usd"
	if native {
		unit = "native"
		price = "CASE WHEN t.value_usd>0 THEN t.price_usd*t.value_native/t.value_usd END"
	}
	trader, join := "NULL::bigint", ""
	priceCondition := "w.complete AND "
	if wallets {
		trader = "coalesce(wc.traders,0)"
		join = " LEFT JOIN wallet_counts wc ON wc.token_address=tokens.token_address AND wc.seconds=w.seconds"
		priceCondition = ""
	}
	return fmt.Sprintf(`SELECT tokens.token_address,w.seconds,w.complete,coalesce(s.trades,0) AS trades,coalesce(s.buys,0) AS buys,coalesce(s.sells,0) AS sells,
 CASE WHEN s.token_address IS NULL THEN '0' ELSE trim_scale(s.volume_%[1]s)::text END AS volume,
 CASE WHEN s.token_address IS NULL THEN '0' ELSE trim_scale(s.buy_volume_%[1]s)::text END AS buy_volume,
 CASE WHEN s.token_address IS NULL THEN '0' ELSE trim_scale(s.sell_volume_%[1]s)::text END AS sell_volume,
 CASE WHEN s.token_address IS NULL THEN '0' ELSE trim_scale(s.buy_volume_%[1]s-s.sell_volume_%[1]s)::text END AS net_inflow,
 %[2]s AS traders,%[3]s AS price_change_pct
 FROM tokens CROSS JOIN windows w LEFT JOIN stats s ON s.token_address=tokens.token_address AND s.seconds=w.seconds
 %[4]s %[5]s`, unit, trader, fmt.Sprintf(windowChangeSQL, unit), join, fmt.Sprintf(windowPriceJoinsSQL, unit, price, "tokens.token_address", priceCondition))
}

// ChainWindows uses the same SQL as TokenWindows in one database round trip,
// returning every token traded in any of the supplied windows.
func (s *Store) ChainWindows(ctx context.Context, chain string, asOf time.Time, windows []time.Duration, native bool) ([]ChainWindowValues, error) {
	var longest time.Duration
	seconds := make([]int64, len(windows))
	for i, w := range windows {
		seconds[i] = int64(w / time.Second)
		longest = max(longest, w)
	}
	asOf = asOf.UTC().Truncate(time.Minute)
	rows, err := s.pool.Query(ctx, chainWindowsSQL(native), chain, asOf.Add(-longest), asOf, seconds)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ChainWindowValues, error) {
		var v ChainWindowValues
		var sec int64
		err := r.Scan(&v.Address, &sec, &v.Complete, &v.Trades, &v.Buys, &v.Sells, &v.Volume, &v.BuyVolume, &v.SellVolume, &v.NetInflow, &v.Traders, &v.PriceChangePct)
		v.Window = time.Duration(sec) * time.Second
		return v, err
	})
}

var inflowColumns = []string{"projection_version", "chain", "window", "token_address", "symbol", "name", "image_url", "launch_platform", "launch_at",
	"price_usd", "price_native", "market_cap_usd", "market_cap_native", "price_change_pct", "price_change_pct_native", "liquidity_usd", "liquidity_native",
	"trades", "buys", "sells", "observed_trades", "observed_buys", "observed_sells", "coverage", "volume_usd", "volume_native", "net_inflow_usd", "net_inflow_native",
	"sm_holders", "sm_trades", "holders", "age_seconds", "dexes", "socials", "sparkline", "sparkline_native", "dev_holding_pct"}

// collectInflowRows retains the original exact Go path for regression and
// read-only audits. Publication uses the shared server-side array expression.
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

func inflowSelectSQL(long bool) string {
	sparkCTEs := ""
	sparkJoin := ` LEFT JOIN LATERAL (` + inflowSparklineSQL() + `) spark ON true`
	extra := `,NULL::int[],NULL::numeric,NULL::numeric`
	if long {
		sparkCTEs = longSparklineCTEs()
		sparkJoin = ` LEFT JOIN long_series spark ON spark.token_address=t.address LEFT JOIN LATERAL (
   SELECT c.c AS usd,c.c_native AS native FROM candles c WHERE c.chain=$1 AND c.token_address=t.address AND c.interval='1m'
    AND c.t<$2 AND (c.c IS NOT NULL OR c.c_native IS NOT NULL) ORDER BY c.t DESC LIMIT 1
  )seed ON true`
		extra = `,spark.buckets,seed.usd,seed.native`
	}
	return chainWindowsCTEs(false) + sparkCTEs + `, usd AS (` + chainWindowsSelect(false, false) + `), native AS (` + chainWindowsSelect(true, false) + `),
 smart AS (
  SELECT token_address,count(*) AS trades,bool_or(value_usd IS NULL) AS unpriced_usd,bool_or(value_native IS NULL) AS unpriced_native,
   sum(CASE WHEN side='buy' THEN value_usd ELSE -value_usd END) AS usd,
   sum(CASE WHEN side='buy' THEN value_native ELSE -value_native END) AS native
  FROM sm_trades WHERE version=$6 AND chain=$1 AND block_time>=$2 AND block_time<$3 GROUP BY token_address
 ), dexes AS (SELECT token_address,dexes AS ids FROM stats)

 SELECT $5::bigint,$1::chain_id,$7::text,t.address,coalesce(t.symbol,''),coalesce(t.name,''),t.image_url,t.launch_platform,t.launch_at,
 m.price_usd,m.price_native,m.market_cap_usd,m.market_cap_native,
 CASE WHEN u.complete THEN u.price_change_pct::numeric END,CASE WHEN u.complete THEN n.price_change_pct::numeric END,m.liquidity_usd,m.liquidity_native,
 CASE WHEN u.complete THEN u.trades END,CASE WHEN u.complete THEN u.buys END,CASE WHEN u.complete THEN u.sells END,
 u.trades,u.buys,u.sells,CASE WHEN u.complete THEN 'complete' ELSE 'partial' END,
 CASE WHEN u.complete THEN u.volume::numeric END,CASE WHEN u.complete THEN n.volume::numeric END,
 CASE WHEN u.complete AND NOT coalesce(sm.unpriced_usd,false) THEN coalesce(sm.usd,0) END,
 CASE WHEN u.complete AND NOT coalesce(sm.unpriced_native,false) THEN coalesce(sm.native,0) END,
 CASE WHEN h.coverage<>'unknown' THEN (SELECT count(*) FROM token_balances b JOIN sm_list_members member
  ON member.version=$6 AND member.chain=b.chain AND member.address=b.owner AND NOT member.excluded
  WHERE b.chain=t.chain AND b.token_address=t.address AND b.balance>0 AND is_holder_owner(b.chain,b.owner)) END,
 CASE WHEN u.complete THEN coalesce(sm.trades,0) END,CASE WHEN h.coverage<>'unknown' THEN h.holders END,
 floor(extract(epoch FROM($3::timestamptz-t.launch_at)))::bigint,coalesce(d.ids,'{}'),coalesce(t.socials,'{}'),
 spark.usd,spark.native,
 CASE WHEN h.coverage<>'unknown' AND dev.balance IS NOT NULL
  THEN greatest(dev.balance,0)*10::numeric^(-t.supply_decimals)*100/nullif(m.circulating_supply,0) END` + extra + `
 FROM usd u JOIN native n USING(token_address,seconds) JOIN token_info t ON t.chain=$1 AND t.address=u.token_address
 LEFT JOIN token_market m ON m.chain=t.chain AND m.address=t.address LEFT JOIN smart sm ON sm.token_address=t.address
 LEFT JOIN dexes d ON d.token_address=t.address LEFT JOIN token_holder_state h ON h.chain=t.chain AND h.token_address=t.address
 LEFT JOIN token_balances dev ON dev.chain=t.chain AND dev.token_address=t.address AND dev.owner=t.creator
 ` + sparkJoin
}

// H1's stored 1m closes already use main_pool_at at each underlying trade's
// own timestamp, including switches within a minute. Never reselect that pool
// at the candle's minute boundary. Sub-minute series apply the same function
// directly to each fact. The latest known close also supplies empty buckets.
func inflowSparklineSQL() string {
	return `WITH points(at,usd,native,bn,ti,ix) AS MATERIALIZED (
  SELECT tr.block_time,tr.price_usd,CASE WHEN tr.value_usd>0 THEN tr.price_usd*tr.value_native/tr.value_usd END,
   tr.block_num,tr.tx_index,tr.ix_index FROM trades tr WHERE tr.chain=$1 AND tr.token_address=t.address
   AND tr.block_time>=$2 AND tr.block_time<$3 AND $7 IN ('1m','5m') AND tr.price_usd IS NOT NULL
   AND tr.pool_id=main_pool_at(tr.chain,tr.token_address,tr.block_time)
  UNION ALL
  SELECT tr.block_time,tr.price_usd,CASE WHEN tr.value_usd>0 THEN tr.price_usd*tr.value_native/tr.value_usd END,
   tr.block_num,tr.tx_index,tr.ix_index FROM LATERAL (
   SELECT tr.* FROM trades tr WHERE tr.chain=$1 AND tr.token_address=t.address AND tr.block_time<$2
    AND $7 IN ('1m','5m') AND tr.price_usd IS NOT NULL AND tr.pool_id=main_pool_at(tr.chain,tr.token_address,tr.block_time)
   ORDER BY tr.block_time DESC,tr.block_num DESC,tr.tx_index DESC,tr.ix_index DESC LIMIT 1
  )tr
 ), closes AS MATERIALIZED (
  SELECT DISTINCT ON(bucket) bucket,usd,native FROM(
   SELECT CASE WHEN at<$2::timestamptz THEN -1 ELSE floor(extract(epoch FROM(at-$2::timestamptz))*20/extract(epoch FROM($3::timestamptz-$2::timestamptz)))::int END AS bucket,* FROM points
  )p ORDER BY bucket,at DESC,bn DESC,ti DESC,ix DESC
 )
 SELECT array_agg(last.usd ORDER BY b.bucket) AS usd,array_agg(last.native ORDER BY b.bucket) AS native
 FROM generate_series(0,19)b(bucket) LEFT JOIN LATERAL (
  SELECT c.usd,c.native FROM closes c WHERE c.bucket<=b.bucket ORDER BY c.bucket DESC LIMIT 1
 )last ON true`
}

// projectionReadiness uses the existing compute-owned parse marks and pricing
// queues. Coverage alone cannot prove that the derived facts caught up.
func projectionReadiness(ctx context.Context, tx pgx.Tx, chain string, from, to time.Time) (string, error) {
	var stats, smart bool
	err := tx.QueryRow(ctx, `SELECT
 NOT EXISTS(SELECT 1 FROM chain_blocks b WHERE b.chain=$1 AND b.block_time>=$2 AND b.block_time<$3
  AND b.parsed_at>coalesce((SELECT at FROM compute_marks WHERE chain=$1 AND name='stats'),'-infinity'::timestamptz))
 AND NOT EXISTS(SELECT 1 FROM stats_dirty WHERE chain=$1 AND minute>=$2 AND minute<$3),
 NOT EXISTS(SELECT 1 FROM chain_blocks b WHERE b.chain=$1 AND b.block_time>=$2 AND b.block_time<$3
  AND b.parsed_at>coalesce((SELECT at FROM compute_marks WHERE chain=$1 AND name='smtrades'),'-infinity'::timestamptz))
 AND NOT EXISTS(SELECT 1 FROM smtrades_dirty WHERE chain=$1 AND minute>=$2 AND minute<$3)`, chain, from, to).Scan(&stats, &smart)
	if err != nil {
		return "", err
	}
	if !stats {
		return "stats_not_ready", nil
	}
	if !smart {
		return "smtrades_not_ready", nil
	}
	return "", nil
}

// longSparklineCTEs returns at most twenty sparse closes per token. The final
// indexed LATERAL join supplies the seed; Go only carries exact observations.
func longSparklineCTEs() string {
	return `, long_closes AS MATERIALIZED (
  SELECT DISTINCT ON(token_address,bucket) token_address,bucket,usd,native FROM (
   SELECT c.token_address,c.t,c.c AS usd,c.c_native AS native,
    floor(extract(epoch FROM(c.t-$2::timestamptz))*20/extract(epoch FROM($3::timestamptz-$2::timestamptz)))::int AS bucket
   FROM candles c JOIN tokens tok ON tok.token_address=c.token_address
   WHERE c.chain=$1 AND c.interval='1m' AND c.t>=$2 AND c.t<$3 AND (c.c IS NOT NULL OR c.c_native IS NOT NULL)
  )p ORDER BY token_address,bucket,t DESC
 ), long_series AS MATERIALIZED (
  SELECT token_address,array_agg(bucket ORDER BY bucket) AS buckets,
   array_agg(usd ORDER BY bucket) AS usd,array_agg(native ORDER BY bucket) AS native
  FROM long_closes GROUP BY token_address
 )`
}

// ErrProjectionPending means activation requires a new projection before reads.
var ErrProjectionPending = errors.New("store: inflow projection rebuild pending")

// InflowKey identifies a batch token without depending on wire address formats.
type InflowKey struct {
	Chain   string
	Address []byte
}

// InflowHead holds one published chain cutoff within a statement snapshot.
type InflowHead struct {
	Window string
	Chain  string
	AsOf   time.Time
}

// InflowDataRow holds persisted values in the selected currency. Dexes remain
// IDs; their display names and wire address encoding belong to the Backend.
type InflowDataRow struct {
	Chain                                            string
	Address                                          []byte
	Symbol, Name                                     string
	ImageURL, Platform                               *string
	LaunchAt                                         *time.Time
	Price, MarketCap, Change, Liquidity, Volume, Net *string
	Trades, Buys, Sells                              *int64
	ObservedTrades, ObservedBuys, ObservedSells      int64
	Coverage                                         string
	SMHolders, SMTrades, Holders, Age                *int64
	Dexes                                            []string
	Socials                                          []byte
	Sparkline                                        []*string
	DevHoldingPct                                    *string
}

// InflowSnapshot binds rows, all selected heads and active membership to one
// repeatable-read snapshot. A nil key slice means all rows; an empty slice none.
type InflowSnapshot struct {
	ListVersion int64
	Heads       []InflowHead
	Rows        []InflowDataRow
}

// ReadInflow reads only complete published versions, never a mixture across
// an activation. With no head yet it preserves H2's known empty response.
func (s *Store) ReadInflow(ctx context.Context, chain, window string, native bool, keys []InflowKey) (InflowSnapshot, error) {
	out := InflowSnapshot{Rows: []InflowDataRow{}}
	chains := make([]string, len(keys))
	addresses := make([][]byte, len(keys))
	for i, k := range keys {
		chains[i] = k.Chain
		addresses[i] = k.Address
	}
	batch := keys != nil
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT active.version,h.chain::text,h.as_of,h.list_version,h.rebuild_pending
 FROM(SELECT version FROM sm_lists WHERE status='active')active LEFT JOIN projection_heads h
 ON h.name='inflow' AND h."window"=$2 AND ($1='' OR h.chain::text=$1) AND (NOT $3 OR h.chain::text=ANY($4::text[]))`, chain, window, batch, chains)
	if err != nil {
		return out, err
	}
	pending := false
	for rows.Next() {
		var ch *string
		var at *time.Time
		var list *int64
		var waiting *bool
		if err = rows.Scan(&out.ListVersion, &ch, &at, &list, &waiting); err != nil {
			rows.Close()
			return out, err
		}
		if ch != nil {
			out.Heads = append(out.Heads, InflowHead{Chain: *ch, AsOf: at.UTC()})
			pending = pending || *waiting || *list != out.ListVersion
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if pending {
		return out, ErrProjectionPending
	}
	if len(out.Heads) == 0 {
		return out, tx.Commit(ctx)
	}
	rows, err = tx.Query(ctx, `SELECT r.chain::text,r.token_address,r.symbol,r.name,r.image_url,r.launch_platform,r.launch_at,
 trim_scale(CASE WHEN $3::boolean THEN r.price_native ELSE r.price_usd END)::text,
 trim_scale(CASE WHEN $3::boolean THEN r.market_cap_native ELSE r.market_cap_usd END)::text,
 trim_scale(CASE WHEN $3::boolean THEN r.price_change_pct_native ELSE r.price_change_pct END)::text,
 trim_scale(CASE WHEN $3::boolean THEN r.liquidity_native ELSE r.liquidity_usd END)::text,
 r.trades,r.buys,r.sells,r.observed_trades,r.observed_buys,r.observed_sells,r.coverage,
 trim_scale(CASE WHEN $3::boolean THEN r.volume_native ELSE r.volume_usd END)::text,
 trim_scale(CASE WHEN $3::boolean THEN r.net_inflow_native ELSE r.net_inflow_usd END)::text,
 r.sm_holders,r.sm_trades,r.holders,r.age_seconds,r.dexes,r.socials,
 ARRAY(SELECT trim_scale(x)::text FROM unnest(CASE WHEN $3::boolean THEN r.sparkline_native ELSE r.sparkline END)x),trim_scale(r.dev_holding_pct)::text
 FROM projection_heads h JOIN inflow_rows r ON r.projection_version=h.projection_version AND r.chain=h.chain AND r."window"=h."window"
 WHERE h.name='inflow' AND h."window"=$2 AND ($1='' OR h.chain::text=$1)
 AND (NOT $6 OR EXISTS(SELECT 1 FROM unnest($4::text[],$5::bytea[])k(chain,address) WHERE k.chain=r.chain::text AND k.address=r.token_address))
 ORDER BY r.chain::text COLLATE "C",r.token_address`, chain, window, native, chains, addresses, batch)
	if err != nil {
		return out, err
	}
	out.Rows, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (InflowDataRow, error) {
		var v InflowDataRow
		err := r.Scan(&v.Chain, &v.Address, &v.Symbol, &v.Name, &v.ImageURL, &v.Platform, &v.LaunchAt, &v.Price, &v.MarketCap, &v.Change, &v.Liquidity,
			&v.Trades, &v.Buys, &v.Sells, &v.ObservedTrades, &v.ObservedBuys, &v.ObservedSells, &v.Coverage, &v.Volume, &v.Net,
			&v.SMHolders, &v.SMTrades, &v.Holders, &v.Age, &v.Dexes, &v.Socials, &v.Sparkline, &v.DevHoldingPct)
		return v, err
	})
	if err != nil {
		return out, fmt.Errorf("store: read inflow rows: %w", err)
	}
	return out, tx.Commit(ctx)
}

// fillInflowBuckets carries exact SQL numeric observations; it never computes
// prices or converts them through a floating-point representation.
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

// InflowFacet holds one exact DEX/platform aggregate at the published cutoffs.
type InflowFacet struct {
	Kind, Chain, ID string
	Volume          *string
	Count           *int64
	Observed        int64
}

// InflowFacets binds membership, cutoffs and aggregates to one read snapshot.
func (s *Store) InflowFacets(ctx context.Context, chain, window string) ([]InflowFacet, bool, error) {
	width, err := time.ParseDuration(window)
	if err != nil {
		return nil, false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	var heads int
	var ready, complete bool
	err = tx.QueryRow(ctx, `SELECT count(*),coalesce(bool_and(NOT h.rebuild_pending AND h.list_version=a.version),false),
 coalesce(bool_and(EXISTS(SELECT 1 FROM coverage_ranges cr WHERE cr.chain=h.chain AND cr.from_time<=h.as_of-$3*interval '1 second' AND cr.to_time>=h.as_of)),false)
 FROM projection_heads h CROSS JOIN(SELECT version FROM sm_lists WHERE status='active')a
 WHERE h.name='inflow' AND h."window"=$2 AND ($1='' OR h.chain::text=$1)`, chain, window, int64(width/time.Second)).Scan(&heads, &ready, &complete)
	if err != nil {
		return nil, false, err
	}
	if heads == 0 || !ready {
		return nil, false, ErrProjectionPending
	}
	if chain == "" && heads != 3 {
		complete = false
	}
	rows, err := tx.Query(ctx, `WITH heads AS MATERIALIZED (
 SELECT * FROM projection_heads WHERE name='inflow' AND "window"=$2 AND ($1='' OR chain::text=$1)
 ), current AS MATERIALIZED (
 SELECT r.* FROM heads h JOIN inflow_rows r ON r.projection_version=h.projection_version AND r.chain=h.chain AND r."window"=h."window"
 ), dex_stats AS (
 SELECT ps.chain,ps.dex,count(DISTINCT ps.token_address) AS tokens,
 CASE WHEN NOT bool_or(ps.unpriced_trades>0) THEN sum(ps.volume_usd) END AS volume
 FROM heads h JOIN pool_minute_stats ps ON ps.chain=h.chain AND ps.minute>=h.as_of-$3*interval '1 second' AND ps.minute<h.as_of
 GROUP BY ps.chain,ps.dex
 ), dex_observed AS (
 SELECT r.chain,id,count(DISTINCT r.token_address) AS tokens FROM current r CROSS JOIN LATERAL unnest(r.dexes)id GROUP BY r.chain,id
 ), facets AS (
 SELECT 'dex' AS kind,coalesce(d.chain,o.chain)::text AS chain,coalesce(d.dex,o.id) AS id,d.volume,coalesce(d.tokens,0) AS tokens,coalesce(o.tokens,0) AS observed
 FROM dex_stats d FULL JOIN dex_observed o ON d.chain=o.chain AND d.dex=o.id
 UNION ALL
 SELECT 'platform',chain::text,launch_platform,CASE WHEN count(*) FILTER(WHERE volume_usd IS NULL)=0 THEN sum(volume_usd) END,count(*),count(*)
 FROM current WHERE launch_platform IS NOT NULL GROUP BY chain,launch_platform
 ) SELECT kind,chain,id,trim_scale(volume)::text,tokens,observed FROM facets
 ORDER BY kind,volume DESC NULLS LAST,chain COLLATE "C",id COLLATE "C"`, chain, window, int64(width/time.Second))
	if err != nil {
		return nil, false, err
	}
	values, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (InflowFacet, error) {
		var v InflowFacet
		err := r.Scan(&v.Kind, &v.Chain, &v.ID, &v.Volume, &v.Count, &v.Observed)
		if !complete {
			v.Volume, v.Count = nil, nil
		}
		return v, err
	})
	if err != nil {
		return nil, false, err
	}
	return values, complete, tx.Commit(ctx)
}

// InflowHeads returns persisted cutoffs for compute's backlog metrics.
func (s *Store) InflowHeads(ctx context.Context) ([]InflowHead, error) {
	rows, err := s.pool.Query(ctx, `SELECT chain::text,"window",as_of FROM projection_heads WHERE name='inflow' ORDER BY chain,"window"`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (InflowHead, error) {
		var v InflowHead
		err := r.Scan(&v.Chain, &v.Window, &v.AsOf)
		return v, err
	})
}

// inflowBucketSQL copies exact sparse observations into twenty array elements.
func inflowBucketSQL(seed, buckets, prices string) string {
	elements := make([]string, 20)
	for bucket := range elements {
		index := fmt.Sprintf("width_bucket(%d,%s)", bucket, buckets)
		elements[bucket] = fmt.Sprintf("CASE WHEN coalesce(%s,0)=0 THEN %s ELSE (%s)[%s] END", index, seed, prices, index)
	}
	return "ARRAY[" + strings.Join(elements, ",") + "]"
}

// inflowPublishSelectSQL is shared by the publisher and read-only audit. Long
// series use twenty scalar array elements, without a SQL row grid or sorting.
func inflowPublishSelectSQL(source string, long bool) string {
	aliases := make([]string, len(inflowColumns)+3)
	fields := make([]string, len(inflowColumns))
	for i := range aliases {
		aliases[i] = fmt.Sprintf("c%d", i)
	}
	for i := range fields {
		fields[i] = "raw." + aliases[i]
	}
	if long {
		fields[34] = inflowBucketSQL("raw.c38", "raw.c37", "raw.c34")
		fields[35] = inflowBucketSQL("raw.c39", "raw.c37", "raw.c35")
	}
	return "SELECT " + strings.Join(fields, ",") + " FROM (" + source + ") raw(" + strings.Join(aliases, ",") + ")"
}
