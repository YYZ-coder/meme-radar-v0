SELECT raw.c0,raw.c1,raw.c2,raw.c3,raw.c4,raw.c5,raw.c6,raw.c7,raw.c8,raw.c9,raw.c10,raw.c11,raw.c12,raw.c13,raw.c14,raw.c15,raw.c16,raw.c17,raw.c18,raw.c19,raw.c20,raw.c21,raw.c22,raw.c23,raw.c24,raw.c25,raw.c26,raw.c27,raw.c28,raw.c29,raw.c30,raw.c31,raw.c32,raw.c33,raw.c34,raw.c35,raw.c36 FROM (WITH bind AS (SELECT $6::bigint list_version), windows AS MATERIALIZED (
  SELECT w.*,EXISTS (SELECT 1 FROM coverage_ranges cr WHERE cr.chain=$1 AND cr.from_time<=w.from_time AND cr.to_time >= $3) AS complete FROM(
   SELECT seconds,n,$3::timestamptz-seconds*interval '1 second' AS from_time
   FROM unnest($4::bigint[]) WITH ORDINALITY w(seconds,n)
  )w
 ), stats AS MATERIALIZED (
  SELECT ps.token_address,w.seconds,coalesce(sum(ps.trades),0)::bigint AS trades, coalesce(sum(ps.buys),0)::bigint AS buys,
			coalesce(sum(ps.sells),0)::bigint AS sells, CASE WHEN NOT coalesce(bool_or(ps.unpriced_trades>0),false) THEN coalesce(sum(ps.volume_usd),0) END AS volume_usd,
			CASE WHEN count(*) FILTER (WHERE ps.buy_volume_usd IS NULL)=0 THEN coalesce(sum(ps.buy_volume_usd),0) END AS buy_volume_usd,
			CASE WHEN count(*) FILTER (WHERE ps.sell_volume_usd IS NULL)=0 THEN coalesce(sum(ps.sell_volume_usd),0) END AS sell_volume_usd, CASE WHEN NOT coalesce(bool_or(ps.unpriced_trades>0),false) THEN coalesce(sum(ps.volume_native),0) END AS volume_native,
			CASE WHEN count(*) FILTER (WHERE ps.buy_volume_native IS NULL)=0 THEN coalesce(sum(ps.buy_volume_native),0) END AS buy_volume_native,
			CASE WHEN count(*) FILTER (WHERE ps.sell_volume_native IS NULL)=0 THEN coalesce(sum(ps.sell_volume_native),0) END AS sell_volume_native,
   array_agg(DISTINCT ps.dex ORDER BY ps.dex) AS dexes
  FROM windows w JOIN pool_minute_stats ps ON ps.chain=$1 AND ps.minute>=w.from_time AND ps.minute<$3
  GROUP BY ps.token_address,w.seconds
 ), tokens AS MATERIALIZED (SELECT DISTINCT token_address FROM stats WHERE $2::timestamptz IS NOT NULL), usd AS (SELECT tokens.token_address,w.seconds,w.complete,coalesce(s.trades,0) AS trades,coalesce(s.buys,0) AS buys,coalesce(s.sells,0) AS sells,
 CASE WHEN s.token_address IS NULL THEN '0' ELSE trim_scale(s.volume_usd)::text END AS volume,
 CASE WHEN s.token_address IS NULL THEN '0' ELSE trim_scale(s.buy_volume_usd)::text END AS buy_volume,
 CASE WHEN s.token_address IS NULL THEN '0' ELSE trim_scale(s.sell_volume_usd)::text END AS sell_volume,
 CASE WHEN s.token_address IS NULL THEN '0' ELSE trim_scale(s.buy_volume_usd-s.sell_volume_usd)::text END AS net_inflow,
 NULL::bigint AS traders,trim_scale(round(round((tm.price_usd - coalesce(previous.price,first_inside.price))*100,
   scale((tm.price_usd - coalesce(previous.price,first_inside.price))*100)+40) /
   nullif(coalesce(previous.price,first_inside.price),0),20))::text AS price_change_pct
 FROM tokens CROSS JOIN windows w LEFT JOIN stats s ON s.token_address=tokens.token_address AND s.seconds=w.seconds
   LEFT JOIN token_market tm ON tm.chain=$1 AND tm.address=tokens.token_address
 LEFT JOIN LATERAL (
  SELECT t.price_usd AS price FROM trades t
  WHERE w.complete AND  t.chain=$1 AND t.token_address=tokens.token_address AND t.block_time<w.from_time AND t.price_usd IS NOT NULL
   AND t.pool_id=main_pool_at(t.chain,t.token_address,t.block_time) AND (t.price_usd) IS NOT NULL
  ORDER BY t.block_time DESC,t.block_num DESC,t.tx_index DESC,t.ix_index DESC LIMIT 1
 ) previous ON true
 LEFT JOIN LATERAL (
  SELECT t.price_usd AS price FROM trades t
  WHERE w.complete AND  previous.price IS NULL AND t.chain=$1 AND t.token_address=tokens.token_address AND t.block_time>=w.from_time AND t.block_time<$3
   AND t.price_usd IS NOT NULL AND t.pool_id=main_pool_at(t.chain,t.token_address,t.block_time) AND (t.price_usd) IS NOT NULL
  ORDER BY t.block_time,t.block_num,t.tx_index,t.ix_index LIMIT 1
 ) first_inside ON true), native AS (SELECT tokens.token_address,w.seconds,w.complete,coalesce(s.trades,0) AS trades,coalesce(s.buys,0) AS buys,coalesce(s.sells,0) AS sells,
 CASE WHEN s.token_address IS NULL THEN '0' ELSE trim_scale(s.volume_native)::text END AS volume,
 CASE WHEN s.token_address IS NULL THEN '0' ELSE trim_scale(s.buy_volume_native)::text END AS buy_volume,
 CASE WHEN s.token_address IS NULL THEN '0' ELSE trim_scale(s.sell_volume_native)::text END AS sell_volume,
 CASE WHEN s.token_address IS NULL THEN '0' ELSE trim_scale(s.buy_volume_native-s.sell_volume_native)::text END AS net_inflow,
 NULL::bigint AS traders,trim_scale(round(round((tm.price_native - coalesce(previous.price,first_inside.price))*100,
   scale((tm.price_native - coalesce(previous.price,first_inside.price))*100)+40) /
   nullif(coalesce(previous.price,first_inside.price),0),20))::text AS price_change_pct
 FROM tokens CROSS JOIN windows w LEFT JOIN stats s ON s.token_address=tokens.token_address AND s.seconds=w.seconds
   LEFT JOIN token_market tm ON tm.chain=$1 AND tm.address=tokens.token_address
 LEFT JOIN LATERAL (
  SELECT CASE WHEN t.value_usd>0 THEN t.price_usd*t.value_native/t.value_usd END AS price FROM trades t
  WHERE w.complete AND  t.chain=$1 AND t.token_address=tokens.token_address AND t.block_time<w.from_time AND t.price_usd IS NOT NULL
   AND t.pool_id=main_pool_at(t.chain,t.token_address,t.block_time) AND (CASE WHEN t.value_usd>0 THEN t.price_usd*t.value_native/t.value_usd END) IS NOT NULL
  ORDER BY t.block_time DESC,t.block_num DESC,t.tx_index DESC,t.ix_index DESC LIMIT 1
 ) previous ON true
 LEFT JOIN LATERAL (
  SELECT CASE WHEN t.value_usd>0 THEN t.price_usd*t.value_native/t.value_usd END AS price FROM trades t
  WHERE w.complete AND  previous.price IS NULL AND t.chain=$1 AND t.token_address=tokens.token_address AND t.block_time>=w.from_time AND t.block_time<$3
   AND t.price_usd IS NOT NULL AND t.pool_id=main_pool_at(t.chain,t.token_address,t.block_time) AND (CASE WHEN t.value_usd>0 THEN t.price_usd*t.value_native/t.value_usd END) IS NOT NULL
  ORDER BY t.block_time,t.block_num,t.tx_index,t.ix_index LIMIT 1
 ) first_inside ON true),
 smart AS (SELECT NULL::bytea token_address,0::bigint trades,false unpriced_usd,false unpriced_native,NULL::numeric usd,NULL::numeric native WHERE false
 ), dexes AS (SELECT token_address,dexes AS ids FROM stats)

 SELECT $5::bigint,$1::chain_id,$7::text,t.address,coalesce(t.symbol,''),coalesce(t.name,''),NULL::text,t.launch_platform,t.launch_at,
 m.price_usd,m.price_native,m.market_cap_usd,m.market_cap_native,
 CASE WHEN u.complete THEN u.price_change_pct::numeric END,CASE WHEN u.complete THEN n.price_change_pct::numeric END,m.liquidity_usd,m.liquidity_native,
 CASE WHEN u.complete THEN u.trades END,CASE WHEN u.complete THEN u.buys END,CASE WHEN u.complete THEN u.sells END,
 u.trades,u.buys,u.sells,CASE WHEN u.complete THEN 'complete' ELSE 'partial' END,
 CASE WHEN u.complete THEN u.volume::numeric END,CASE WHEN u.complete THEN n.volume::numeric END,
 CASE WHEN u.complete AND NOT coalesce(sm.unpriced_usd,false) THEN coalesce(sm.usd,0) END,
 CASE WHEN u.complete AND NOT coalesce(sm.unpriced_native,false) THEN coalesce(sm.native,0) END,
 NULL::bigint,
 CASE WHEN u.complete THEN coalesce(sm.trades,0) END,CASE WHEN h.coverage<>'unknown' THEN h.holders END,
 floor(extract(epoch FROM($3::timestamptz-t.launch_at)))::bigint,coalesce(d.ids,'{}'),coalesce('{}'::jsonb,'{}'),
 spark.usd,spark.native,
 CASE WHEN h.coverage<>'unknown' THEN greatest(dev.balance,0)*10::numeric^(-t.supply_decimals)*100/nullif(m.circulating_supply,0) END,NULL::int[],NULL::numeric,NULL::numeric
 FROM usd u JOIN native n USING(token_address,seconds) JOIN token_info t ON t.chain=$1 AND t.address=u.token_address
 LEFT JOIN token_market m ON m.chain=t.chain AND m.address=t.address LEFT JOIN smart sm ON sm.token_address=t.address
 LEFT JOIN dexes d ON d.token_address=t.address LEFT JOIN LATERAL(SELECT 'unknown'::text coverage,NULL::bigint holders)h ON true
 LEFT JOIN LATERAL(SELECT NULL::numeric balance)dev ON true
  LEFT JOIN LATERAL (WITH points(at,usd,native,bn,ti,ix) AS MATERIALIZED (
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
 )last ON true) spark ON true) raw(c0,c1,c2,c3,c4,c5,c6,c7,c8,c9,c10,c11,c12,c13,c14,c15,c16,c17,c18,c19,c20,c21,c22,c23,c24,c25,c26,c27,c28,c29,c30,c31,c32,c33,c34,c35,c36,c37,c38,c39)