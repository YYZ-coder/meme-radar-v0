WITH windows AS (
		SELECT seconds, n, $3::timestamptz - seconds * interval '1 second' AS from_time
		FROM unnest($4::bigint[]) WITH ORDINALITY AS w(seconds, n)
	)
	SELECT w.seconds,
		EXISTS (SELECT 1 FROM coverage_ranges cr WHERE cr.chain=$1 AND cr.from_time<=w.from_time AND cr.to_time >= $3),
		v.trades, v.buys, v.sells, trim_scale(v.volume)::text, trim_scale(v.buy)::text, trim_scale(v.sell)::text,
		trim_scale(v.buy-v.sell)::text, wallets.traders,
  trim_scale(round(round((tm.price_%[1]s - coalesce(previous.price,first_inside.price))*100,
   scale((tm.price_%[1]s - coalesce(previous.price,first_inside.price))*100)+40) /
   nullif(coalesce(previous.price,first_inside.price),0),20))::text
	FROM windows w CROSS JOIN LATERAL (
		SELECT coalesce(sum(ps.trades),0)::bigint AS trades, coalesce(sum(ps.buys),0)::bigint AS buys,
			coalesce(sum(ps.sells),0)::bigint AS sells,
			CASE WHEN NOT coalesce(bool_or(ps.unpriced_trades>0),false) THEN coalesce(sum(ps.volume_%[1]s),0) END AS volume,
			CASE WHEN count(*) FILTER (WHERE ps.buy_volume_%[1]s IS NULL)=0 THEN coalesce(sum(ps.buy_volume_%[1]s),0) END AS buy,
			CASE WHEN count(*) FILTER (WHERE ps.sell_volume_%[1]s IS NULL)=0 THEN coalesce(sum(ps.sell_volume_%[1]s),0) END AS sell
		FROM pool_minute_stats ps WHERE ps.chain=$1 AND ps.token_address=$2 AND ps.minute>=w.from_time AND ps.minute<$3
	) v CROSS JOIN LATERAL (
		SELECT count(DISTINCT t.wallet) AS traders FROM trades t
		WHERE t.chain=$1 AND t.token_address=$2 AND t.block_time>=w.from_time AND t.block_time<$3
	) wallets
 LEFT JOIN token_market tm ON tm.chain=$1 AND tm.address=$2
 LEFT JOIN LATERAL (
  SELECT %[2]s AS price FROM trades t
  WHERE t.chain=$1 AND t.token_address=$2 AND t.block_time<w.from_time AND t.price_usd IS NOT NULL
   AND t.pool_id=main_pool_at(t.chain,t.token_address,t.block_time) AND (%[2]s) IS NOT NULL
  ORDER BY t.block_time DESC,t.block_num DESC,t.tx_index DESC,t.ix_index DESC LIMIT 1
 ) previous ON true
 LEFT JOIN LATERAL (
  SELECT %[2]s AS price FROM trades t
  WHERE previous.price IS NULL AND t.chain=$1 AND t.token_address=$2 AND t.block_time>=w.from_time AND t.block_time<$3
   AND t.price_usd IS NOT NULL AND t.pool_id=main_pool_at(t.chain,t.token_address,t.block_time) AND (%[2]s) IS NOT NULL
  ORDER BY t.block_time,t.block_num,t.tx_index,t.ix_index LIMIT 1
 ) first_inside ON true
 ORDER BY w.n