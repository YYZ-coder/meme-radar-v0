package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const chainAsOfSQL = `SELECT CASE WHEN max(to_time) IS NOT NULL
		THEN date_trunc('minute', least($2::timestamptz, max(to_time))) END
		FROM coverage_ranges WHERE chain = $1`

// ChainAsOf returns the latest covered cutoff, capped by now and rounded down
// to a UTC minute. The boolean is false when the chain has no timed coverage.
func (s *Store) ChainAsOf(ctx context.Context, chain string, now time.Time) (time.Time, bool, error) {
	var cutoff *time.Time
	err := s.pool.QueryRow(ctx, chainAsOfSQL, chain, now.UTC()).Scan(&cutoff)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("store: chain cutoff: %w", err)
	}
	if cutoff == nil {
		return time.Time{}, false, nil
	}
	return cutoff.UTC(), true, nil
}

// Coverage reports whether one contiguous processed range covers [from, to).
func (s *Store) Coverage(ctx context.Context, chain string, from, to time.Time) (bool, error) {
	var complete bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM coverage_ranges
		WHERE chain = $1 AND from_time <= $2 AND to_time >= $3)`, chain, from.UTC(), to.UTC()).Scan(&complete)
	if err != nil {
		return false, fmt.Errorf("store: window coverage: %w", err)
	}
	return complete, nil
}

// WindowValues holds observed all-pool counts and nullable amounts. Complete
// describes chain coverage; partial coverage does not erase observed values.
type WindowValues struct {
	Window                                   time.Duration
	Complete                                 bool
	Trades, Buys, Sells                      int64
	Volume, BuyVolume, SellVolume, NetInflow *string
	Traders                                  *int64
	PriceChangePct                           *string
}

// TokenWindows returns minute-aligned, half-open all-pool window statistics in
// input order. native selects native amounts instead of USD amounts.
func (s *Store) TokenWindows(ctx context.Context, chain string, token []byte, asOf time.Time, windows []time.Duration, native bool) ([]WindowValues, error) {
	seconds := make([]int64, len(windows))
	for i, w := range windows {
		seconds[i] = int64(w / time.Second)
	}
	query := tokenWindowQuery(native)
	rows, err := s.pool.Query(ctx, query, chain, token, asOf.UTC().Truncate(time.Minute), seconds, native)
	if err != nil {
		return nil, fmt.Errorf("store: token windows: %w", err)
	}
	return collectTokenWindows(rows)
}

func tokenWindowQuery(native bool) string {
	unit := "usd"
	if native {
		unit = "native"
	}
	return fmt.Sprintf(windowValuesSQL, unit, nativeTradePriceSQL, "$2", windowCountsSQL+", "+fmt.Sprintf(windowAmountsSQL, unit), fmt.Sprintf(windowPriceJoinsSQL, unit, nativeTradePriceSQL, "$2", ""), fmt.Sprintf(windowChangeSQL, unit))
}

func collectTokenWindows(rows pgx.Rows) ([]WindowValues, error) {
	values, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (WindowValues, error) {
		var v WindowValues
		var seconds int64
		err := r.Scan(&seconds, &v.Complete, &v.Trades, &v.Buys, &v.Sells, &v.Volume, &v.BuyVolume, &v.SellVolume, &v.NetInflow, &v.Traders, &v.PriceChangePct)
		v.Window = time.Duration(seconds) * time.Second
		return v, err
	})
	if err != nil {
		return nil, fmt.Errorf("store: token windows: %w", err)
	}
	return values, nil
}

// All windows end at the same cutoff, so a wallet contributes exactly when
// its latest trade is inside that window. Materializing this single bounded
// scan avoids sorting/scanning the same overlapping raw trades four times.
// The third format argument is a trusted selected-token SQL expression.
const windowValuesSQL = `WITH windows AS (
		SELECT seconds, n, $3::timestamptz - seconds * interval '1 second' AS from_time
		FROM unnest($4::bigint[]) WITH ORDINALITY AS w(seconds, n)
	), wallet_last AS MATERIALIZED (
		SELECT t.wallet, max(t.block_time) AS last_at FROM trades t
		WHERE t.chain=$1 AND t.token_address=%[3]s AND t.wallet IS NOT NULL
		AND t.block_time>=(SELECT min(from_time) FROM windows) AND t.block_time<$3
		GROUP BY t.wallet
	)
	SELECT w.seconds,
		EXISTS (SELECT 1 FROM coverage_ranges cr WHERE cr.chain=$1 AND cr.from_time<=w.from_time AND cr.to_time >= $3),
		v.trades, v.buys, v.sells, trim_scale(v.volume_%[1]s)::text, trim_scale(v.buy_volume_%[1]s)::text, trim_scale(v.sell_volume_%[1]s)::text,
		trim_scale(v.buy_volume_%[1]s-v.sell_volume_%[1]s)::text, wallets.traders,
  %[6]s
	FROM windows w CROSS JOIN LATERAL (
		SELECT %[4]s
		FROM pool_minute_stats ps WHERE ps.chain=$1 AND ps.token_address=%[3]s AND ps.minute>=w.from_time AND ps.minute<$3
	) v CROSS JOIN LATERAL (
		SELECT count(*) AS traders FROM wallet_last wl WHERE wl.last_at>=w.from_time
	) wallets
 %[5]s
 ORDER BY w.n`

const nativeTradePriceSQL = `CASE WHEN $5::boolean THEN CASE WHEN t.value_usd>0 THEN t.price_usd*t.value_native/t.value_usd END ELSE t.price_usd END`

const windowCountsSQL = `coalesce(sum(ps.trades),0)::bigint AS trades, coalesce(sum(ps.buys),0)::bigint AS buys,
			coalesce(sum(ps.sells),0)::bigint AS sells`

const windowAmountsSQL = `CASE WHEN NOT coalesce(bool_or(ps.unpriced_trades>0),false) THEN coalesce(sum(ps.volume_%[1]s),0) END AS volume_%[1]s,
			CASE WHEN count(*) FILTER (WHERE ps.buy_volume_%[1]s IS NULL)=0 THEN coalesce(sum(ps.buy_volume_%[1]s),0) END AS buy_volume_%[1]s,
			CASE WHEN count(*) FILTER (WHERE ps.sell_volume_%[1]s IS NULL)=0 THEN coalesce(sum(ps.sell_volume_%[1]s),0) END AS sell_volume_%[1]s`

const windowPriceJoinsSQL = ` LEFT JOIN token_market tm ON tm.chain=$1 AND tm.address=%[3]s
 LEFT JOIN LATERAL (
  SELECT %[2]s AS price FROM trades t
  WHERE %[4]s t.chain=$1 AND t.token_address=%[3]s AND t.block_time<w.from_time AND t.price_usd IS NOT NULL
   AND t.pool_id=main_pool_at(t.chain,t.token_address,t.block_time) AND (%[2]s) IS NOT NULL
  ORDER BY t.block_time DESC,t.block_num DESC,t.tx_index DESC,t.ix_index DESC LIMIT 1
 ) previous ON true
 LEFT JOIN LATERAL (
  SELECT %[2]s AS price FROM trades t
  WHERE %[4]s previous.price IS NULL AND t.chain=$1 AND t.token_address=%[3]s AND t.block_time>=w.from_time AND t.block_time<$3
   AND t.price_usd IS NOT NULL AND t.pool_id=main_pool_at(t.chain,t.token_address,t.block_time) AND (%[2]s) IS NOT NULL
  ORDER BY t.block_time,t.block_num,t.tx_index,t.ix_index LIMIT 1
 ) first_inside ON true`

const windowChangeSQL = `trim_scale(round(round((tm.price_%[1]s - coalesce(previous.price,first_inside.price))*100,
   scale((tm.price_%[1]s - coalesce(previous.price,first_inside.price))*100)+40) /
   nullif(coalesce(previous.price,first_inside.price),0),20))::text`

const windowCoverageSQL = `EXISTS (SELECT 1 FROM coverage_ranges cr WHERE cr.chain=$1 AND cr.from_time<=w.from_time AND cr.to_time >= $3)`

// mainPoolTimeSQL prices graduation's effective time from chain evidence. The
// candidate casts keep the shared batch parameters typed for both statements.
const mainPoolTimeSQL = `WITH switch_time AS (
 SELECT CASE WHEN $6='graduation' THEN least($7::timestamptz,greatest(
  (SELECT max(ps.t) FROM pool_switches ps WHERE ps.chain=$1 AND ps.token_address=$2),
  coalesce((SELECT p.created_at FROM pools p WHERE p.chain=$1 AND p.pool_id=$3),
   (SELECT min(t.block_time) FROM trades t WHERE t.chain=$1 AND t.token_address=$2 AND t.pool_id=$3),$7::timestamptz)
 )) ELSE $7::timestamptz END AS at, $4::bytea AS candidate, $5::timestamptz AS candidate_since
)`

// ErrRangeTooLong reports a sub-minute candle range older than 24 hours.
var ErrRangeTooLong = errors.New("store: candle range exceeds 24 hours")

// PoolSwitchRow is one main-pool transition, ordered by T and Seq.
// From is nil for the first allocation.
type PoolSwitchRow struct {
	T        time.Time
	Seq      int64
	From, To []byte
	Reason   string
}

// Bar is a time bucket with nullable OHLC and USD volume, including on native charts.
type Bar struct {
	T          time.Time
	O, H, L, C *string
	V          *string
	Closed     bool
}

// CandleSet includes observed bars and the coverage and pool history needed
// by callers to distinguish complete closed bars from partial observations.
type CandleSet struct {
	Bars        []Bar
	Complete    bool
	Switches    []PoolSwitchRow
	Gaps        [][2]time.Time
	EmptyReason string
}

// TokenCandles returns the latest limit bars in [from,to), in time order.
// Sub-minute intervals are restricted to the last 24 hours.
func (s *Store) TokenCandles(ctx context.Context, chain string, token []byte, interval, metric string, native bool, from, to time.Time, limit int, now time.Time) (CandleSet, error) {
	width := map[string]string{"1s": "1 second", "30s": "30 seconds", "1m": "1 minute", "5m": "5 minutes", "1h": "1 hour", "1d": "1 day"}[interval]
	if width == "" {
		return CandleSet{}, fmt.Errorf("store: unsupported candle interval %q", interval)
	}
	if metric != "price" && metric != "market_cap" {
		return CandleSet{}, fmt.Errorf("store: unsupported candle metric %q", metric)
	}
	subminute := interval == "1s" || interval == "30s"
	if subminute && from.Before(now.Add(-24*time.Hour)) {
		return CandleSet{}, ErrRangeTooLong
	}
	out := CandleSet{}
	complete, err := s.Coverage(ctx, chain, from, to)
	if err != nil {
		return out, err
	}
	out.Complete = complete
	source := storedCandlesSQL
	if subminute {
		source = tradeCandlesSQL
	}
	query := fmt.Sprintf(candleSelectSQL, source)
	rows, err := s.pool.Query(ctx, query, chain, token, from.UTC(), to.UTC(), width, native, limit, now.UTC(), metric, interval)
	if err != nil {
		return out, fmt.Errorf("store: trade candles: %w", err)
	}
	out.Bars, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Bar, error) {
		var b Bar
		err := r.Scan(&b.T, &b.O, &b.H, &b.L, &b.C, &b.V, &b.Closed)
		b.T = b.T.UTC()
		return b, err
	})
	if err != nil {
		return out, fmt.Errorf("store: trade candles: %w", err)
	}
	if err := s.candleMetadata(ctx, chain, token, width, from, to, &out); err != nil {
		return out, err
	}
	return out, nil
}

// candleMetadata describes missing USD evidence independently of native price
// and metric selection; nullable market-cap bars also carry no_price.
func (s *Store) candleMetadata(ctx context.Context, chain string, token []byte, width string, from, to time.Time, out *CandleSet) error {
	var hasPool bool
	var trades, priced int64
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM token_market WHERE chain=$1 AND address=$2 AND main_pool_id IS NOT NULL),
  count(*),count(t.price_usd) FROM trades t WHERE t.chain=$1 AND t.token_address=$2 AND t.block_time>=$3 AND t.block_time<$4
  AND t.pool_id=main_pool_at(t.chain,t.token_address,t.block_time)`, chain, token, from, to).Scan(&hasPool, &trades, &priced)
	if err != nil {
		return fmt.Errorf("store: candle availability: %w", err)
	}
	switch {
	case !hasPool:
		out.EmptyReason = "no_main_pool"
	case trades == 0:
		out.EmptyReason = "no_trades"
	case priced == 0:
		out.EmptyReason = "no_price"
	default:
		if len(out.Bars) > 0 {
			known := false
			for _, b := range out.Bars {
				known = known || b.O != nil || b.H != nil || b.L != nil || b.C != nil
			}
			if !known {
				out.EmptyReason = "no_price"
			}
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT t,seq,from_pool,to_pool,reason FROM pool_switches
  WHERE chain=$1 AND token_address=$2 AND t>=$3 AND t<$4 ORDER BY t,seq`, chain, token, from, to)
	if err != nil {
		return fmt.Errorf("store: candle switches: %w", err)
	}
	out.Switches, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (PoolSwitchRow, error) {
		var sw PoolSwitchRow
		err := r.Scan(&sw.T, &sw.Seq, &sw.From, &sw.To, &sw.Reason)
		sw.T = sw.T.UTC()
		return sw, err
	})
	if err != nil {
		return fmt.Errorf("store: candle switches: %w", err)
	}
	rows, err = s.pool.Query(ctx, `WITH buckets AS (
  SELECT date_bin($5::interval,t.block_time,'2000-01-01 00:00:00+00'::timestamptz) AS t
  FROM trades t WHERE t.chain=$1 AND t.token_address=$2 AND t.block_time>=$3 AND t.block_time<$4
   AND t.pool_id=main_pool_at(t.chain,t.token_address,t.block_time)
  GROUP BY 1 HAVING count(t.price_usd)=0
 ), islands AS (
  SELECT t,t-row_number()OVER(ORDER BY t)*$5::interval AS island FROM buckets WHERE t>=$3 AND t<$4
 ) SELECT min(t),max(t)+$5::interval FROM islands GROUP BY island ORDER BY min(t)`, chain, token, from, to, width)
	if err != nil {
		return fmt.Errorf("store: candle gaps: %w", err)
	}
	out.Gaps, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) ([2]time.Time, error) {
		var g [2]time.Time
		err := r.Scan(&g[0], &g[1])
		g[0] = g[0].UTC()
		g[1] = g[1].UTC()
		return g, err
	})
	if err != nil {
		return fmt.Errorf("store: candle gaps: %w", err)
	}
	return nil
}

const tradeCandlesSQL = `WITH facts AS MATERIALIZED (
 SELECT t.*,date_bin($5::interval,t.block_time,'2000-01-01 00:00:00+00'::timestamptz) AS bucket,
  CASE WHEN $6::boolean THEN CASE WHEN t.value_usd>0 THEN t.price_usd*t.value_native/t.value_usd END ELSE t.price_usd END AS price
 FROM trades t WHERE t.chain=$1 AND t.token_address=$2 AND t.block_time>=$3 AND t.block_time<$4 AND $10::text IN ('1s','30s')
), volume AS (
 SELECT bucket,sum(value_usd) AS v FROM facts GROUP BY bucket
), bars AS (
 SELECT bucket AS t,
  (array_agg(price ORDER BY block_time,block_num,tx_index,ix_index))[1] AS o,
  max(price) AS h,min(price) AS l,
  (array_agg(price ORDER BY block_time DESC,block_num DESC,tx_index DESC,ix_index DESC))[1] AS c
 FROM facts WHERE price_usd IS NOT NULL AND pool_id=main_pool_at(chain,token_address,block_time)
 GROUP BY bucket
), latest AS (
 SELECT k.t,k.o,k.h,k.l,k.c,v.v,
  k.t+$5::interval<=$8 AS closed
 FROM bars k JOIN volume v ON v.bucket=k.t WHERE k.t>=$3 AND k.t<$4
 ORDER BY k.t DESC LIMIT $7
) SELECT * FROM latest ORDER BY t`

const storedCandlesSQL = `SELECT t,
 CASE WHEN $6::boolean THEN o_native ELSE o END AS o,
 CASE WHEN $6::boolean THEN h_native ELSE h END AS h,
 CASE WHEN $6::boolean THEN l_native ELSE l END AS l,
 CASE WHEN $6::boolean THEN c_native ELSE c END AS c,v,
 t+$5::interval<=$8 AS closed
 FROM candles WHERE chain=$1 AND token_address=$2 AND interval=$10 AND t>=$3 AND t<$4
 ORDER BY t DESC LIMIT $7`

const candleSelectSQL = `WITH raw AS (%s)
 SELECT k.t,trim_scale(k.o*f.value)::text,trim_scale(k.h*f.value)::text,
  trim_scale(k.l*f.value)::text,trim_scale(k.c*f.value)::text,trim_scale(k.v)::text,k.closed
 FROM raw k LEFT JOIN token_market tm ON tm.chain=$1 AND tm.address=$2
 CROSS JOIN LATERAL (SELECT CASE WHEN $9::text='market_cap' THEN tm.circulating_supply ELSE 1 END AS value)f
 ORDER BY k.t`

// RefreshATH advances chain's recorded highs from retained 1m and 1d candles.
func (s *Store) RefreshATH(ctx context.Context, chain string) error {
	_, err := s.pool.Exec(ctx, `WITH highs AS (
  SELECT DISTINCT ON(token_address) token_address,h,t FROM candles
  WHERE chain=$1 AND interval IN ('1m','1d')
  ORDER BY token_address,h DESC,CASE interval WHEN '1m' THEN 0 ELSE 1 END,t
 ) INSERT INTO token_market(chain,address,ath_price_usd,ath_market_cap_usd,ath_at)
 SELECT $1,h.token_address,h.h,h.h*tm.circulating_supply,h.t FROM highs h
 LEFT JOIN token_market tm ON tm.chain=$1 AND tm.address=h.token_address
 ON CONFLICT(chain,address)DO UPDATE SET ath_price_usd=EXCLUDED.ath_price_usd,
  ath_market_cap_usd=EXCLUDED.ath_market_cap_usd,ath_at=EXCLUDED.ath_at
 WHERE token_market.ath_price_usd IS NULL OR EXCLUDED.ath_price_usd>token_market.ath_price_usd`, chain)
	if err != nil {
		return fmt.Errorf("store: refresh ATH: %w", err)
	}
	return nil
}
