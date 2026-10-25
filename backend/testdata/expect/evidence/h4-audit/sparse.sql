WITH bind AS (SELECT $6::bigint list_version), windows AS MATERIALIZED (
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
 ), tokens AS MATERIALIZED (SELECT DISTINCT token_address FROM stats WHERE $2::timestamptz IS NOT NULL), long_closes AS MATERIALIZED (
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
 ), usd AS (SELECT tokens.token_address,w.seconds,w.complete,coalesce(s.trades,0) AS trades,coalesce(s.buys,0) AS buys,coalesce(s.sells,0) AS sells,
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
 CASE WHEN h.coverage<>'unknown' THEN greatest(dev.balance,0)*10::numeric^(-t.supply_decimals)*100/nullif(m.circulating_supply,0) END,spark.buckets,seed.usd,seed.native
 FROM usd u JOIN native n USING(token_address,seconds) JOIN token_info t ON t.chain=$1 AND t.address=u.token_address
 LEFT JOIN token_market m ON m.chain=t.chain AND m.address=t.address LEFT JOIN smart sm ON sm.token_address=t.address
 LEFT JOIN dexes d ON d.token_address=t.address LEFT JOIN LATERAL(SELECT 'unknown'::text coverage,NULL::bigint holders)h ON true
 LEFT JOIN LATERAL(SELECT NULL::numeric balance)dev ON true
  LEFT JOIN long_series spark ON spark.token_address=t.address LEFT JOIN LATERAL (
   SELECT c.c AS usd,c.c_native AS native FROM candles c WHERE c.chain=$1 AND c.token_address=t.address AND c.interval='1m'
    AND c.t<$2 AND (c.c IS NOT NULL OR c.c_native IS NOT NULL) ORDER BY c.t DESC LIMIT 1
  )seed ON true