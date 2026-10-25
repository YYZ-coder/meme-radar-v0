-- Preserve the order of switches whose effective chain time is identical.
ALTER TABLE pool_switches ADD COLUMN seq bigint GENERATED ALWAYS AS IDENTITY;
ALTER TABLE pool_switches DROP CONSTRAINT pool_switches_pkey;
ALTER TABLE pool_switches ADD PRIMARY KEY (chain, token_address, t, seq);

-- H1: the canonical historical main pool, shared by windows and candles.
CREATE FUNCTION main_pool_at(p_chain chain_id, p_token bytea, p_at timestamptz)
RETURNS bytea LANGUAGE sql STABLE AS $$
 SELECT coalesce(
  (SELECT to_pool FROM pool_switches WHERE chain=p_chain AND token_address=p_token AND t<=p_at ORDER BY t DESC, seq DESC LIMIT 1),
  (SELECT to_pool FROM pool_switches WHERE chain=p_chain AND token_address=p_token ORDER BY t, seq LIMIT 1)
 )
$$;

-- Correct historical graduation timestamps in their original switch order.
-- Use each preceding corrected timestamp as the lower bound, so consecutive
-- graduations remain monotonic even when several are clamped to the same t.
WITH RECURSIVE ordered AS MATERIALIZED (
 SELECT ps.*, row_number() OVER (PARTITION BY ps.chain, ps.token_address ORDER BY ps.t, ps.seq) AS n,
  coalesce(p.created_at,
   (SELECT min(tr.block_time) FROM trades tr
    WHERE tr.chain=ps.chain AND tr.token_address=ps.token_address AND tr.pool_id=ps.to_pool), ps.t) AS evidence_at
 FROM pool_switches ps
 LEFT JOIN pools p ON p.chain=ps.chain AND p.pool_id=ps.to_pool
), corrected AS (
 SELECT chain, token_address, seq, t AS old_t, n,
  CASE WHEN reason='graduation' THEN least(now(), evidence_at) ELSE t END AS new_t
 FROM ordered WHERE n=1
 UNION ALL
 SELECT o.chain, o.token_address, o.seq, o.t, o.n,
  CASE WHEN o.reason='graduation' THEN least(now(), greatest(c.new_t, o.evidence_at)) ELSE o.t END
 FROM ordered o JOIN corrected c
  ON o.chain=c.chain AND o.token_address=c.token_address AND o.n=c.n+1
), changed AS (
 UPDATE pool_switches ps SET t=c.new_t FROM corrected c
 WHERE ps.chain=c.chain AND ps.token_address=c.token_address AND ps.t=c.old_t AND ps.seq=c.seq
  AND ps.t IS DISTINCT FROM c.new_t
 RETURNING ps.chain, c.old_t, c.new_t
)
INSERT INTO stats_dirty(chain,minute)
 SELECT c.chain, g.minute FROM changed c
 CROSS JOIN LATERAL generate_series(
  date_trunc('minute',greatest(least(c.old_t,c.new_t),now()-interval '3 days')),
  date_trunc('minute',least(greatest(c.old_t,c.new_t),now())),interval '1 minute') AS g(minute)
 WHERE g.minute>now()-interval '3 days'
 ON CONFLICT DO NOTHING;

-- Per-trade pool selection also changes the single minute of an unchanged
-- liquidity/graduation switch. Older minutes are outside RefreshStats' 3-day
-- retention; first allocations have no old pool and do not need rebuilding.
INSERT INTO stats_dirty(chain,minute)
 SELECT chain,date_trunc('minute',t) FROM pool_switches
 WHERE from_pool IS NOT NULL AND t<=now() AND date_trunc('minute',t)>now()-interval '3 days'
 ON CONFLICT DO NOTHING;

-- Compute owns ATH alongside the other token market projections.
ALTER TABLE token_market
 ADD COLUMN ath_price_usd numeric,
 ADD COLUMN ath_market_cap_usd numeric,
 ADD COLUMN ath_at timestamptz;
