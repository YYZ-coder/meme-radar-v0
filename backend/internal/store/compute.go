package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// QuotePrice is the USD price of a quote asset for one minute.
type QuotePrice struct {
	Symbol string
	Minute time.Time
	USD    string
	Source string
}

// SaveQuotePrices upserts per-minute prices; a later save for the same
// minute replaces the earlier one (the live price becomes the minute close).
func (s *Store) SaveQuotePrices(ctx context.Context, ps []QuotePrice) error {
	batch := &pgx.Batch{}
	for _, p := range ps {
		batch.Queue(`INSERT INTO quote_prices (symbol, minute, price_usd, source) VALUES ($1, $2, $3, $4)
			ON CONFLICT (symbol, minute) DO UPDATE SET price_usd = EXCLUDED.price_usd, source = EXCLUDED.source, updated_at = now()`,
			p.Symbol, p.Minute.UTC().Truncate(time.Minute), p.USD, p.Source)
	}
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("store: save quote prices: %w", err)
	}
	return nil
}

// MissingPriceMinutes returns, oldest first, the minutes since since in which
// chain has unpriced trades or liquidity events but symbol (the chain's
// native coin, needed for every USD and native value) has no price.
func (s *Store) MissingPriceMinutes(ctx context.Context, chain, symbol string, since time.Time) ([]time.Time, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m FROM (
			SELECT date_trunc('minute', block_time) AS m FROM trades WHERE chain = $1 AND value_usd IS NULL AND block_time >= $3
			UNION
			SELECT date_trunc('minute', block_time) FROM liquidity_events WHERE chain = $1 AND value_usd IS NULL AND block_time >= $3
		) x
		WHERE NOT EXISTS (SELECT 1 FROM quote_prices q WHERE q.symbol = $2 AND q.minute = x.m)
		ORDER BY m`, chain, symbol, since)
	if err != nil {
		return nil, fmt.Errorf("store: missing price minutes: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (time.Time, error) {
		var t time.Time
		err := r.Scan(&t)
		return t.UTC(), err
	})
	if err != nil {
		return nil, fmt.Errorf("store: missing price minutes: %w", err)
	}
	return out, nil
}

// QuoteAsset is a registered quote asset: native (priced from quote_prices)
// or stable (1 USD).
type QuoteAsset struct {
	Chain    string
	Asset    []byte
	Symbol   string
	Kind     string // native, stable
	Decimals int16
}

func (s *Store) SaveQuoteAssets(ctx context.Context, qs []QuoteAsset) error {
	batch := &pgx.Batch{}
	for _, q := range qs {
		batch.Queue(`INSERT INTO quote_assets (chain, asset, symbol, kind, decimals) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (chain, asset) DO UPDATE SET symbol = EXCLUDED.symbol, kind = EXCLUDED.kind, decimals = EXCLUDED.decimals`,
			q.Chain, q.Asset, q.Symbol, q.Kind, q.Decimals)
	}
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("store: save quote assets: %w", err)
	}
	return nil
}

// nativePrice is the chain's native USD price for the fact's minute, or the
// latest earlier minute within 5 minutes (D135: older prices are not used).
const nativePrice = `JOIN LATERAL (SELECT q.price_usd FROM quote_prices q WHERE q.symbol = $2
		AND q.minute <= date_trunc('minute', f.block_time) AND q.minute > date_trunc('minute', f.block_time) - interval '5 minutes'
		ORDER BY q.minute DESC LIMIT 1) np ON true`

// factQuote prices fact f's pool quote (pool p) in USD at the fact's time:
// a registered native coin at np, a stablecoin at 1, or one hop through a
// tracked token whose main pool is quoted in a registered asset, at that
// token's last priced main-pool trade within the hour before the fact.
// It yields u.dec (quote decimals), u.usd (NULL when unpriced) and qa.kind.
const factQuote = `LEFT JOIN quote_assets qa ON qa.chain = f.chain AND qa.asset = p.quote_asset
			LEFT JOIN LATERAL (
				SELECT qt.decimals, tr.price_usd FROM tokens qt
				JOIN token_market tm ON tm.chain = qt.chain AND tm.address = qt.address
				JOIN pools mp ON mp.chain = qt.chain AND mp.pool_id = tm.main_pool_id
				JOIN quote_assets mqa ON mqa.chain = mp.chain AND mqa.asset = mp.quote_asset
				JOIN LATERAL (SELECT t2.price_usd FROM trades t2 WHERE t2.chain = qt.chain AND t2.token_address = qt.address
					AND t2.pool_id = tm.main_pool_id AND t2.price_usd IS NOT NULL
					AND t2.block_time <= f.block_time AND t2.block_time > f.block_time - interval '1 hour'
					ORDER BY t2.block_time DESC LIMIT 1) tr ON true
				WHERE qa.asset IS NULL AND qt.chain = f.chain AND qt.address = p.quote_asset AND qt.decimals IS NOT NULL
			) hop ON true
			CROSS JOIN LATERAL (SELECT coalesce(qa.decimals, hop.decimals) AS dec,
				CASE qa.kind WHEN 'native' THEN np.price_usd WHEN 'stable' THEN 1 ELSE hop.price_usd END AS usd) u`

// FillValues sets the USD values of chain's unpriced trades and liquidity
// events since since whose pool quote can be priced (factQuote) and whose
// minute has a native price; the rest stay NULL for a later pass (D138).
// Trades get value_usd, price_usd and, for non-native quotes, value_native;
// liquidity events count both sides: the quote amount plus the token amount
// at the token's last priced trade, value_native = value_usd / native price.
func (s *Store) FillValues(ctx context.Context, chain, native string, since time.Time) error {
	_, err := s.pool.Exec(ctx, `
		WITH upd AS (
		UPDATE trades t SET value_usd = v.usd, price_usd = v.price_usd, value_native = v.native
		FROM (
			SELECT f.block_time, f.block_num, f.tx_index, f.ix_index,
				f.quote_amount * 10::numeric ^ (-u.dec) * u.usd AS usd,
				f.price_native * u.usd AS price_usd,
				CASE WHEN qa.kind = 'native' THEN coalesce(f.value_native, f.quote_amount * 10::numeric ^ (-u.dec))
					ELSE f.quote_amount * 10::numeric ^ (-u.dec) * u.usd / np.price_usd END AS native
			FROM trades f
			JOIN pools p ON p.chain = f.chain AND p.pool_id = f.pool_id
			`+nativePrice+`
			`+factQuote+`
			WHERE f.chain = $1 AND f.value_usd IS NULL AND f.block_time >= $3 AND u.usd IS NOT NULL
		) v
		WHERE t.chain = $1 AND t.block_time = v.block_time AND t.block_num = v.block_num AND t.tx_index = v.tx_index AND t.ix_index = v.ix_index
		RETURNING date_trunc('minute', t.block_time) AS minute, t.token_address
		), minutes AS (
			INSERT INTO stats_dirty (chain, minute) SELECT DISTINCT $1::chain_id, minute FROM upd ON CONFLICT DO NOTHING
		), smminutes AS (
			INSERT INTO smtrades_dirty (chain, minute) SELECT DISTINCT $1::chain_id, minute FROM upd ON CONFLICT DO NOTHING
		)
		INSERT INTO market_dirty (chain, token_address) SELECT DISTINCT $1::chain_id, token_address FROM upd ON CONFLICT DO NOTHING`,
		chain, native, since)
	if err != nil {
		return fmt.Errorf("store: fill trade values: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE liquidity_events l SET value_usd = v.usd, value_native = v.usd / v.native_usd
		FROM (
			SELECT f.block_time, f.block_num, f.tx_index, f.ix_index, np.price_usd AS native_usd,
				coalesce(f.quote_amount, 0) * 10::numeric ^ (-u.dec) * u.usd
				+ CASE WHEN f.token_amount = 0 THEN 0 ELSE f.token_amount * 10::numeric ^ (-tk.decimals) * tp.price_usd END AS usd
			FROM liquidity_events f
			JOIN pools p ON p.chain = f.chain AND p.pool_id = f.pool_id
			`+nativePrice+`
			`+factQuote+`
			LEFT JOIN tokens tk ON tk.chain = f.chain AND tk.address = f.token_address
			LEFT JOIN LATERAL (SELECT tr.price_usd FROM trades tr WHERE tr.chain = f.chain AND tr.token_address = f.token_address
				AND tr.block_time <= f.block_time AND tr.price_usd IS NOT NULL ORDER BY tr.block_time DESC LIMIT 1) tp ON true
			WHERE f.chain = $1 AND f.value_usd IS NULL AND f.block_time >= $3 AND u.usd IS NOT NULL
		) v
		WHERE v.usd IS NOT NULL AND l.chain = $1 AND l.block_time = v.block_time AND l.block_num = v.block_num
			AND l.tx_index = v.tx_index AND l.ix_index = v.ix_index`,
		chain, native, since)
	if err != nil {
		return fmt.Errorf("store: fill liquidity values: %w", err)
	}
	return nil
}

// quoteDecimals is the pool's quote decimals: a registered quote asset, or
// a tracked token used as quote.
const quoteDecimals = `JOIN LATERAL (
		SELECT qa.decimals FROM quote_assets qa WHERE qa.chain = p.chain AND qa.asset = p.quote_asset
		UNION ALL SELECT tk.decimals FROM tokens tk WHERE tk.chain = p.chain AND tk.address = p.quote_asset AND tk.decimals IS NOT NULL
		LIMIT 1) qd ON true`

// SampleLiquidity records the liquidity of chain's pools whose reserves
// changed since the last sample, at the minute of the reserve state.
func (s *Store) SampleLiquidity(ctx context.Context, chain string) error {
	_, err := s.pool.Exec(ctx, `
		WITH changed AS (
			SELECT p.chain, p.pool_id, p.state_at, p.reserve_quote * 10::numeric ^ (-qd.decimals) * 2 AS lq
			FROM pools p `+quoteDecimals+`
			LEFT JOIN pool_market pm ON pm.chain = p.chain AND pm.pool_id = p.pool_id
			WHERE p.chain = $1 AND p.reserve_quote IS NOT NULL AND p.state_at IS NOT NULL
				AND p.state_at IS DISTINCT FROM pm.liquidity_state_at
		), sampled AS (
			INSERT INTO pool_liquidity (chain, pool_id, minute, liquidity_quote)
			SELECT chain, pool_id, date_trunc('minute', state_at), lq FROM changed
			ON CONFLICT (chain, pool_id, minute) DO UPDATE SET liquidity_quote = EXCLUDED.liquidity_quote
		)
		INSERT INTO pool_market (chain, pool_id, liquidity_quote, liquidity_state_at) SELECT chain, pool_id, lq, state_at FROM changed
		ON CONFLICT (chain, pool_id) DO UPDATE SET liquidity_quote = EXCLUDED.liquidity_quote, liquidity_state_at = EXCLUDED.liquidity_state_at`, chain)
	if err != nil {
		return fmt.Errorf("store: sample liquidity: %w", err)
	}
	return nil
}

// PriceLiquidity sets chain's pool liquidity in USD and native coin at now:
// the latest sample and its 1-hour time-weighted average (falling back to
// the latest sample) times the quote asset's current price. Quotes priced
// from the native coin need a price within 5 minutes of now (D135).
func (s *Store) PriceLiquidity(ctx context.Context, chain, native string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		WITH np AS (
			SELECT q.price_usd FROM quote_prices q WHERE q.symbol = $2 AND q.minute <= $3 AND q.minute > $3 - interval '5 minutes'
			ORDER BY q.minute DESC LIMIT 1
		), avg AS (
			SELECT pool_id, sum(liquidity_quote * w) / nullif(sum(w), 0) AS lq FROM (
				SELECT pool_id, liquidity_quote,
					extract(epoch FROM least(coalesce(lead(minute) OVER (PARTITION BY pool_id ORDER BY minute), $3), $3)
						- greatest(minute, $3 - interval '1 hour')) AS w
				FROM pool_liquidity WHERE chain = $1 AND minute <= $3 AND minute > $3 - interval '1 day'
			) x WHERE w > 0 GROUP BY pool_id
		), quote AS (
			-- a registered quote, or one hop through a tracked token (its current price)
			SELECT pm.pool_id, qa.kind,
				CASE qa.kind WHEN 'native' THEN (SELECT price_usd FROM np) WHEN 'stable' THEN 1 ELSE hop.price_usd END AS usd
			FROM pool_market pm JOIN pools p ON p.chain = pm.chain AND p.pool_id = pm.pool_id
			LEFT JOIN quote_assets qa ON qa.chain = p.chain AND qa.asset = p.quote_asset
			LEFT JOIN LATERAL (
				SELECT tm.price_usd FROM token_market tm
				JOIN pools mp ON mp.chain = tm.chain AND mp.pool_id = tm.main_pool_id
				JOIN quote_assets mqa ON mqa.chain = mp.chain AND mqa.asset = mp.quote_asset
				WHERE qa.asset IS NULL AND tm.chain = p.chain AND tm.address = p.quote_asset
			) hop ON true
			WHERE pm.chain = $1 AND pm.liquidity_quote IS NOT NULL
		), rate AS (
			SELECT pool_id, usd, CASE WHEN kind = 'native' THEN 1 ELSE usd / (SELECT price_usd FROM np) END AS native
			FROM quote WHERE usd IS NOT NULL
		)
		UPDATE pool_market p SET liquidity_usd = p.liquidity_quote * r.usd, liquidity_native = p.liquidity_quote * r.native,
			liquidity_1h_avg_usd = coalesce(a.lq, p.liquidity_quote) * r.usd, liquidity_at = $3
		FROM rate r LEFT JOIN avg a ON a.pool_id = r.pool_id
		WHERE p.chain = $1 AND p.pool_id = r.pool_id`, chain, native, now)
	if err != nil {
		return fmt.Errorf("store: price liquidity: %w", err)
	}
	return nil
}

// PoolRef names a pool and its two assets.
type PoolRef struct{ Pool, Token, Quote []byte }

// PoolsToSample returns chain's pools of the given protocols that traded in
// the 5 minutes before now and have no liquidity sample for now's minute.
func (s *Store) PoolsToSample(ctx context.Context, chain string, protocols []string, now time.Time) ([]PoolRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.pool_id, p.token_address, p.quote_asset FROM pools p
		LEFT JOIN pool_market pm ON pm.chain = p.chain AND pm.pool_id = p.pool_id
		WHERE p.chain = $1 AND p.protocol = ANY($2)
			AND (pm.liquidity_state_at IS NULL OR pm.liquidity_state_at < date_trunc('minute', $3::timestamptz))
			AND EXISTS (SELECT 1 FROM trades t WHERE t.chain = p.chain AND t.token_address = p.token_address AND t.pool_id = p.pool_id
				AND t.block_time > $3::timestamptz - interval '5 minutes' AND t.block_time <= $3)
		ORDER BY p.pool_id`, chain, protocols, now)
	if err != nil {
		return nil, fmt.Errorf("store: pools to sample: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PoolRef, error) {
		var p PoolRef
		err := r.Scan(&p.Pool, &p.Token, &p.Quote)
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("store: pools to sample: %w", err)
	}
	return out, nil
}

// SaveBalanceSample records a pool's liquidity from its token and quote
// balances (atomic units): quote balance + token balance x the pool's last
// trade price, in quote units, at now's minute. Pools whose decimals or
// last price are unknown are skipped.
func (s *Store) SaveBalanceSample(ctx context.Context, chain string, pool []byte, tokenBal, quoteBal string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		WITH v AS (
			SELECT p.chain, p.pool_id, $4::numeric * 10::numeric ^ (-qd.decimals) + $3::numeric * 10::numeric ^ (-tk.decimals) * lt.price_native AS lq
			FROM pools p `+quoteDecimals+`
			JOIN tokens tk ON tk.chain = p.chain AND tk.address = p.token_address AND tk.decimals IS NOT NULL
			JOIN LATERAL (SELECT t.price_native FROM trades t WHERE t.chain = p.chain AND t.token_address = p.token_address AND t.pool_id = p.pool_id
				AND t.price_native IS NOT NULL AND t.block_time <= $5 ORDER BY t.block_time DESC LIMIT 1) lt ON true
			WHERE p.chain = $1 AND p.pool_id = $2
		), sampled AS (
			INSERT INTO pool_liquidity (chain, pool_id, minute, liquidity_quote)
			SELECT chain, pool_id, date_trunc('minute', $5::timestamptz), lq FROM v
			ON CONFLICT (chain, pool_id, minute) DO UPDATE SET liquidity_quote = EXCLUDED.liquidity_quote
		)
		INSERT INTO pool_market (chain, pool_id, liquidity_quote, liquidity_state_at) SELECT chain, pool_id, lq, $5 FROM v
		ON CONFLICT (chain, pool_id) DO UPDATE SET liquidity_quote = EXCLUDED.liquidity_quote, liquidity_state_at = EXCLUDED.liquidity_state_at`,
		chain, pool, tokenBal, quoteBal, now)
	if err != nil {
		return fmt.Errorf("store: save balance sample: %w", err)
	}
	return nil
}

// MainPoolRow is one pool of a token traded in the last 24 hours, with the
// token's main-pool state and the pool's selection scores.
type MainPoolRow struct {
	Token, Main, Candidate []byte
	CandidateSince         *time.Time
	Pool, GraduatedTo      []byte
	Status                 string
	Liquidity1h            *float64
	Volume1h               float64
}

// MainPoolCandidates returns, ordered by token, the pools of chain's tokens
// traded in the 24 hours before now; with onlyNew, only tokens whose trades
// were priced since the last market pass and that have no main pool yet.
func (s *Store) MainPoolCandidates(ctx context.Context, chain string, now time.Time, onlyNew bool) ([]MainPoolRow, error) {
	rows, err := s.pool.Query(ctx, `
		WITH act AS (
			SELECT DISTINCT token_address FROM trades WHERE NOT $3 AND chain = $1 AND block_time > $2::timestamptz - interval '24 hours' AND block_time <= $2
			UNION
			SELECT d.token_address FROM market_dirty d LEFT JOIN token_market tm ON tm.chain = d.chain AND tm.address = d.token_address
			WHERE $3 AND d.chain = $1 AND tm.main_pool_id IS NULL
		)
		-- Each pool's hour volume is an index probe on its token's trades: the
		-- planner underestimates act and would rescan a chain-wide per-pool
		-- aggregate for every pool (26s for 36k Solana pools).
		SELECT p.token_address, tm.main_pool_id, tm.main_candidate, tm.main_candidate_since, p.pool_id, p.graduated_to, p.status,
			pm.liquidity_1h_avg_usd::float8,
			coalesce((SELECT sum(t.value_usd) FROM trades t WHERE t.chain = $1 AND t.token_address = p.token_address AND t.pool_id = p.pool_id
				AND t.block_time > $2::timestamptz - interval '1 hour' AND t.block_time <= $2), 0)::float8
		FROM act JOIN pools p ON p.chain = $1 AND p.token_address = act.token_address
		LEFT JOIN token_market tm ON tm.chain = $1 AND tm.address = p.token_address
		LEFT JOIN pool_market pm ON pm.chain = $1 AND pm.pool_id = p.pool_id
		ORDER BY p.token_address, p.pool_id`, chain, now, onlyNew)
	if err != nil {
		return nil, fmt.Errorf("store: main pool candidates: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (MainPoolRow, error) {
		var m MainPoolRow
		err := r.Scan(&m.Token, &m.Main, &m.Candidate, &m.CandidateSince, &m.Pool, &m.GraduatedTo, &m.Status, &m.Liquidity1h, &m.Volume1h)
		return m, err
	})
	if err != nil {
		return nil, fmt.Errorf("store: main pool candidates: %w", err)
	}
	return out, nil
}

// MainPoolChange is a token's new main-pool state; Reason is set when the
// main pool itself changed (from From).
type MainPoolChange struct {
	Token, From, Pool, Candidate []byte
	CandidateSince               *time.Time
	Reason                       string
}

// SaveMainPools writes main-pool state changes and records pool switches.
func (s *Store) SaveMainPools(ctx context.Context, chain string, cs []MainPoolChange, now time.Time) error {
	batch := &pgx.Batch{}
	for _, c := range cs {
		args := []any{chain, c.Token, c.Pool, c.Candidate, c.CandidateSince, c.Reason, now}
		batch.Queue(mainPoolTimeSQL+`INSERT INTO token_market (chain, address, main_pool_id, main_candidate, main_candidate_since, main_pool_since)
   SELECT $1,$2,$3,st.candidate,st.candidate_since,CASE WHEN $6<>'' THEN st.at END FROM switch_time st
   ON CONFLICT (chain,address) DO UPDATE SET main_pool_id=EXCLUDED.main_pool_id,main_candidate=EXCLUDED.main_candidate,
    main_candidate_since=EXCLUDED.main_candidate_since,
    main_pool_since=CASE WHEN $6<>'' THEN EXCLUDED.main_pool_since ELSE token_market.main_pool_since END`, args...)
		if c.Reason != "" {
			batch.Queue(mainPoolTimeSQL+`INSERT INTO pool_switches (chain,token_address,t,from_pool,to_pool,reason)
    SELECT $1,$2,st.at,$8::bytea,$3,$6 FROM switch_time st`, append(args, c.From)...)

			if c.Reason == "graduation" {
				batch.Queue(mainPoolTimeSQL+`INSERT INTO stats_dirty (chain,minute)
     SELECT $1,m FROM switch_time st CROSS JOIN LATERAL generate_series(
      date_trunc('minute',greatest(st.at,$7::timestamptz-interval '3 days')),
      date_trunc('minute',$7::timestamptz),interval '1 minute') AS g(m)
     ON CONFLICT DO NOTHING`, args...)
			}
		}
	}
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("store: save main pools: %w", err)
	}
	return nil
}

// UpdateMarket sets the current market state of chain's tokens traded in
// the 24 hours before now (with onlyDirty, of those whose trades were priced
// since the last pass): price from the main pool's last priced trade (kept
// when there is none), market cap = price x total supply (token_info: the
// creation event's, else the enrich role's), liquidity = sum
// over active pools (NULL when any is unknown, D56).
func (s *Store) UpdateMarket(ctx context.Context, chain string, now time.Time, onlyDirty bool) error {
	_, err := s.pool.Exec(ctx, `
		WITH target AS (
			SELECT DISTINCT token_address FROM trades WHERE NOT $3 AND chain = $1 AND block_time > $2::timestamptz - interval '24 hours' AND block_time <= $2
			UNION
			SELECT token_address FROM market_dirty WHERE $3 AND chain = $1
		), lt AS (
			SELECT t.token_address, max(t.block_time) AS m FROM target JOIN trades t ON t.chain = $1 AND t.token_address = target.token_address
			WHERE t.block_time > $2::timestamptz - interval '24 hours' AND t.block_time <= $2 GROUP BY t.token_address
		), lp AS (
			-- One newest-first index probe per token instead of sorting all its trades.
			SELECT target.token_address, t.price_usd,
				CASE WHEN t.value_usd > 0 THEN t.price_usd * t.value_native / t.value_usd END AS price_native
			FROM target JOIN token_market tm ON tm.chain = $1 AND tm.address = target.token_address
			CROSS JOIN LATERAL (
				SELECT t.price_usd, t.value_usd, t.value_native FROM trades t
				WHERE t.chain = $1 AND t.token_address = target.token_address AND t.pool_id = tm.main_pool_id
					AND t.price_usd IS NOT NULL AND t.block_time > $2::timestamptz - interval '24 hours' AND t.block_time <= $2
				ORDER BY t.block_time DESC, t.block_num DESC, t.tx_index DESC, t.ix_index DESC LIMIT 1
			) t
		), liq AS (
			SELECT p.token_address,
				CASE WHEN bool_and(pm.liquidity_usd IS NOT NULL) THEN sum(pm.liquidity_usd) END AS usd,
				CASE WHEN bool_and(pm.liquidity_native IS NOT NULL) THEN sum(pm.liquidity_native) END AS native
			FROM target JOIN pools p ON p.chain = $1 AND p.token_address = target.token_address AND p.status = 'active'
			LEFT JOIN pool_market pm ON pm.chain = p.chain AND pm.pool_id = p.pool_id
			GROUP BY p.token_address
		), done AS (
			DELETE FROM market_dirty d USING target WHERE d.chain = $1 AND d.token_address = target.token_address
		)
		INSERT INTO token_market (chain, address, price_usd, price_native, market_cap_usd, market_cap_native, circulating_supply,
			liquidity_usd, liquidity_native, last_trade_at, market_state_at)
		SELECT $1, lt.token_address, lp.price_usd, lp.price_native,
			lp.price_usd * s.supply, lp.price_native * s.supply, s.supply, liq.usd, liq.native, lt.m, $2
		FROM lt JOIN token_info tk ON tk.chain = $1 AND tk.address = lt.token_address
		CROSS JOIN LATERAL (SELECT tk.supply * 10::numeric ^ (-tk.supply_decimals) AS supply) s
		LEFT JOIN lp ON lp.token_address = lt.token_address LEFT JOIN liq ON liq.token_address = lt.token_address
		ON CONFLICT (chain, address) DO UPDATE SET
			price_usd = coalesce(EXCLUDED.price_usd, token_market.price_usd),
			price_native = CASE WHEN EXCLUDED.price_usd IS NULL THEN token_market.price_native ELSE EXCLUDED.price_native END,
			market_cap_usd = coalesce(EXCLUDED.price_usd, token_market.price_usd) * EXCLUDED.circulating_supply,
			market_cap_native = CASE WHEN EXCLUDED.price_usd IS NULL THEN token_market.price_native ELSE EXCLUDED.price_native END
				* EXCLUDED.circulating_supply,
			circulating_supply = EXCLUDED.circulating_supply,
			liquidity_usd = EXCLUDED.liquidity_usd, liquidity_native = EXCLUDED.liquidity_native,
			last_trade_at = EXCLUDED.last_trade_at, market_state_at = EXCLUDED.market_state_at`, chain, now, onlyDirty)
	if err != nil {
		return fmt.Errorf("store: update market: %w", err)
	}
	return nil
}

// statsRetention bounds the minutes pool_minute_stats keeps (3 days).
const statsRetention = 72 * time.Hour

// RefreshStats recomputes chain's pool_minute_stats and candles for every minute whose
// trades changed: minutes of blocks parsed since the last refresh and
// minutes whose trades were priced since (stats_dirty). Each minute is
// rebuilt from trades. A side's volume is 0 without trades on that side and
// NULL when none of them is priced; unpriced_trades counts trades without
// a USD value.
func (s *Store) RefreshStats(ctx context.Context, chain string, now time.Time) error {
	var mark *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT at FROM compute_marks WHERE chain = $1 AND name = 'stats'`, chain).Scan(&mark); err != nil && err != pgx.ErrNoRows {
		return fmt.Errorf("store: stats mark: %w", err)
	}
	var minutes []time.Time
	var newMark *time.Time
	err := s.pool.QueryRow(ctx, `
		WITH parsed AS (
			SELECT date_trunc('minute', block_time) AS m, parsed_at FROM chain_blocks
			WHERE chain = $1 AND parsed_at > coalesce($2::timestamptz, '-infinity'::timestamptz) AND block_time > $3::timestamptz - $4::interval
		)
		SELECT coalesce(array_agg(DISTINCT m ORDER BY m), '{}'), (SELECT max(parsed_at) FROM parsed) FROM (
			SELECT m FROM parsed UNION SELECT minute FROM stats_dirty WHERE chain = $1
		) x WHERE m > $3::timestamptz - $4::interval`, chain, mark, now, statsRetention).Scan(&minutes, &newMark)
	if err != nil {
		return fmt.Errorf("store: stats minutes: %w", err)
	}
	if len(minutes) == 0 {
		return nil
	}
	for _, m := range minutes {
		if err := s.ensurePartition(ctx, "pool_minute_stats", m); err != nil {
			return err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: refresh stats: %w", err)
	}
	defer tx.Rollback(ctx)
	side := func(name, col string) string {
		return fmt.Sprintf(`coalesce(sum(t.%[2]s) FILTER (WHERE t.side = '%[1]s'),
			CASE WHEN count(*) FILTER (WHERE t.side = '%[1]s') = 0 THEN 0 END)`, name, col)
	}
	sqls := []string{
		`DELETE FROM pool_minute_stats WHERE chain = $1 AND minute = ANY($2)`,
		`INSERT INTO pool_minute_stats (chain, pool_id, minute, token_address, dex, trades, buys, sells, volume_usd, buy_volume_usd,
			sell_volume_usd, volume_native, buy_volume_native, sell_volume_native, unpriced_trades)
		SELECT t.chain, t.pool_id, m.minute, p.token_address, p.dex, count(*), count(*) FILTER (WHERE t.side = 'buy'),
			count(*) FILTER (WHERE t.side = 'sell'), sum(t.value_usd), ` + side("buy", "value_usd") + `, ` + side("sell", "value_usd") + `,
			sum(t.value_native), ` + side("buy", "value_native") + `, ` + side("sell", "value_native") + `,
			count(*) FILTER (WHERE t.value_usd IS NULL)
		FROM unnest($2::timestamptz[]) AS m(minute)
		JOIN trades t ON t.block_time >= m.minute AND t.block_time < m.minute + interval '1 minute' AND t.chain = $1
		JOIN pools p ON p.chain = t.chain AND p.pool_id = t.pool_id
		GROUP BY t.chain, t.pool_id, m.minute, p.token_address, p.dex`,
		`DELETE FROM stats_dirty WHERE chain = $1 AND minute = ANY($2)`,
	}
	for _, q := range sqls {
		if _, err := tx.Exec(ctx, q, chain, minutes); err != nil {
			return fmt.Errorf("store: refresh stats: %w", err)
		}
	}
	if err := refreshCandles(ctx, tx, chain, minutes, now); err != nil {
		return err
	}
	if newMark != nil {
		if _, err := tx.Exec(ctx, `INSERT INTO compute_marks (chain, name, at) VALUES ($1, 'stats', $2)
			ON CONFLICT (chain, name) DO UPDATE SET at = EXCLUDED.at`, chain, *newMark); err != nil {
			return fmt.Errorf("store: stats mark: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: refresh stats: %w", err)
	}
	return nil
}

// candleOrigin aligns 5m/1h/1d buckets to UTC.
const candleOrigin = "2000-01-01 00:00:00+00"

// refreshCandles rebuilds the 1m candles of minutes and the 5m, 1h and 1d
// candles containing them. A 1m candle takes OHLC from the priced trades of
// the token's historical main pool at each trade's own time (main_pool_at),
// with the last trade's pool stored on the candle, and v from all pools; native
// prices are price_usd x value_native / value_usd of each trade.
func refreshCandles(ctx context.Context, tx pgx.Tx, chain string, minutes []time.Time, now time.Time) error {
	_, err := tx.Exec(ctx, `DELETE FROM candles WHERE chain = $1 AND interval = '1m' AND t = ANY($2)`, chain, minutes)
	if err != nil {
		return fmt.Errorf("store: refresh candles: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO candles (chain, token_address, interval, t, pool_id, o, h, l, c, o_native, h_native, l_native, c_native, v, closed)
		SELECT $1, x.token_address, '1m', x.minute, (array_agg(x.pool_id ORDER BY x.bt DESC, x.bn DESC, x.ti DESC, x.ii DESC))[1],
			(array_agg(x.pu ORDER BY x.bt, x.bn, x.ti, x.ii))[1], max(x.pu), min(x.pu), (array_agg(x.pu ORDER BY x.bt DESC, x.bn DESC, x.ti DESC, x.ii DESC))[1],
			(array_agg(x.pn ORDER BY x.bt, x.bn, x.ti, x.ii))[1], max(x.pn), min(x.pn), (array_agg(x.pn ORDER BY x.bt DESC, x.bn DESC, x.ti DESC, x.ii DESC))[1],
			min(vol.v), x.minute + interval '1 minute' <= $3
		FROM (
			SELECT m.minute, t.token_address, t.pool_id, t.price_usd AS pu,
				CASE WHEN t.value_usd > 0 THEN t.price_usd * t.value_native / t.value_usd END AS pn,
				t.block_time AS bt, t.block_num AS bn, t.tx_index AS ti, t.ix_index AS ii
			FROM unnest($2::timestamptz[]) AS m(minute)
			JOIN trades t ON t.block_time >= m.minute AND t.block_time < m.minute + interval '1 minute' AND t.chain = $1 AND t.price_usd IS NOT NULL
				AND t.pool_id = main_pool_at(t.chain, t.token_address, t.block_time)
		) x
		JOIN LATERAL (SELECT sum(t.value_usd) AS v FROM trades t WHERE t.chain = $1 AND t.token_address = x.token_address
			AND t.block_time >= x.minute AND t.block_time < x.minute + interval '1 minute') vol ON true
		GROUP BY x.token_address, x.minute`, chain, minutes, now)
	if err != nil {
		return fmt.Errorf("store: refresh 1m candles: %w", err)
	}
	// A minute without a priced main-pool trade has no 1m candle. Read coarse
	// volume from all pool-minute stats so those minutes still contribute.
	for _, iv := range []struct{ name, width string }{{"5m", "5 minutes"}, {"1h", "1 hour"}, {"1d", "1 day"}} {
		// A separate statement: a DELETE in the INSERT's WITH would not be
		// visible to the INSERT's unique check.
		_, err := tx.Exec(ctx, `DELETE FROM candles WHERE chain = $1 AND interval = $4 AND t IN (
			SELECT DISTINCT date_bin($3::interval, m, $5::timestamptz) FROM unnest($2::timestamptz[]) AS u(m))`,
			chain, minutes, iv.width, iv.name, candleOrigin)
		if err != nil {
			return fmt.Errorf("store: refresh %s candles: %w", iv.name, err)
		}
		_, err = tx.Exec(ctx, `
			WITH b AS (SELECT DISTINCT date_bin($3::interval, m, $4::timestamptz) AS t FROM unnest($2::timestamptz[]) AS u(m))
			INSERT INTO candles (chain, token_address, interval, t, pool_id, o, h, l, c, o_native, h_native, l_native, c_native, v, closed)
			SELECT $1, k.token_address, $5, b.t, (array_agg(k.pool_id ORDER BY k.t DESC))[1],
				(array_agg(k.o ORDER BY k.t))[1], max(k.h), min(k.l), (array_agg(k.c ORDER BY k.t DESC))[1],
				(array_agg(k.o_native ORDER BY k.t))[1], max(k.h_native), min(k.l_native), (array_agg(k.c_native ORDER BY k.t DESC))[1],
				(SELECT sum(ps.volume_usd) FROM pool_minute_stats ps
					WHERE ps.chain = $1 AND ps.token_address = k.token_address
					AND ps.minute >= b.t AND ps.minute < b.t + $3::interval), b.t + $3::interval <= $6
			FROM b JOIN candles k ON k.chain = $1 AND k.interval = '1m' AND k.t >= b.t AND k.t < b.t + $3::interval
			GROUP BY k.token_address, b.t`, chain, minutes, iv.width, candleOrigin, iv.name, now)
		if err != nil {
			return fmt.Errorf("store: refresh %s candles: %w", iv.name, err)
		}
	}
	return nil
}

// RefreshCoverage replaces chain's coverage_ranges with the contiguous runs
// of processed blocks in chain_blocks: parsed blocks and skipped slots (D140).
func (s *Store) RefreshCoverage(ctx context.Context, chain string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: refresh coverage: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, q := range []string{
		`DELETE FROM coverage_ranges WHERE chain = $1`,
		`INSERT INTO coverage_ranges (chain, from_block, to_block, from_time, to_time)
		SELECT $1, min(block_num), max(block_num), min(block_time), max(block_time) FROM (
			SELECT block_num, block_time, block_num - row_number() OVER (ORDER BY block_num) AS run
			FROM chain_blocks WHERE chain = $1 AND (status = 'skipped' OR parsed_version IS NOT NULL)
		) b GROUP BY run`,
	} {
		if _, err := tx.Exec(ctx, q, chain); err != nil {
			return fmt.Errorf("store: refresh coverage: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: refresh coverage: %w", err)
	}
	return nil
}

// PruneCompute drops derived rows past their retention (02-data-model §9):
// liquidity samples 1 day, quote prices and 1m candles 30 days, minute
// stats (daily partitions) and pending stats minutes 3 days.
func (s *Store) PruneCompute(ctx context.Context, now time.Time) error {
	for _, q := range []struct{ sql, ago string }{
		{`DELETE FROM pool_liquidity WHERE minute < $1::timestamptz - $2::interval`, "1 day"},
		{`DELETE FROM quote_prices WHERE minute < $1::timestamptz - $2::interval`, "30 days"},
		{`DELETE FROM candles WHERE interval = '1m' AND t < $1::timestamptz - $2::interval`, "30 days"},
		{`DELETE FROM stats_dirty WHERE minute <= $1::timestamptz - $2::interval`, "3 days"},
	} {
		if _, err := s.pool.Exec(ctx, q.sql, now, q.ago); err != nil {
			return fmt.Errorf("store: prune compute: %w", err)
		}
	}
	return s.dropPartitions(ctx, "pool_minute_stats", now.Add(-statsRetention))
}

// TokensToEnrich returns up to limit of chain's tokens traded in the 24
// hours before now that lack creation-event supply or decimals and have no
// supplement yet, or whose last lookup failed more than an hour ago.
func (s *Store) TokensToEnrich(ctx context.Context, chain string, now time.Time, limit int) ([][]byte, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT tk.address FROM tokens tk
		LEFT JOIN token_supplements sp ON sp.chain = tk.chain AND sp.address = tk.address
		WHERE tk.chain = $1 AND (tk.total_supply IS NULL OR tk.decimals IS NULL)
			AND (sp.address IS NULL OR (sp.error IS NOT NULL AND sp.fetched_at <= $2::timestamptz - interval '1 hour'))
			AND EXISTS (SELECT 1 FROM trades t WHERE t.chain = tk.chain AND t.token_address = tk.address
				AND t.block_time > $2::timestamptz - interval '24 hours' AND t.block_time <= $2)
		ORDER BY tk.address LIMIT $3`, chain, now, limit)
	if err != nil {
		return nil, fmt.Errorf("store: tokens to enrich: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		return nil, fmt.Errorf("store: tokens to enrich: %w", err)
	}
	return out, nil
}

// SaveSupplement records a token's looked-up supply, or the lookup error.
func (s *Store) SaveSupplement(ctx context.Context, chain string, token []byte, supply *string, decimals *int16, lookupErr string, at time.Time) error {
	var e *string
	if lookupErr != "" {
		e = &lookupErr
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO token_supplements (chain, address, total_supply, decimals, error, fetched_at) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (chain, address) DO UPDATE SET total_supply = EXCLUDED.total_supply, decimals = EXCLUDED.decimals,
			error = EXCLUDED.error, fetched_at = EXCLUDED.fetched_at`, chain, token, supply, decimals, e, at)
	if err != nil {
		return fmt.Errorf("store: save supplement: %w", err)
	}
	return nil
}

// Backlog is a chain's outstanding work, for the monitoring gauges.
type Backlog struct {
	ForwardLag, GapBlocks, Unparsed, Trades24h, Unpriced24h int64
}

// Backlog reads chain's backlog at now.
func (s *Store) Backlog(ctx context.Context, chain string, now time.Time) (Backlog, error) {
	var b Backlog
	err := s.pool.QueryRow(ctx, `SELECT
		coalesce((SELECT greatest(head - position, 0) FROM stream_cursors WHERE chain = $1 AND stream = 'forward'), 0),
		coalesce((SELECT sum(to_pos - from_pos + 1) FROM stream_gaps WHERE chain = $1 AND status <> 'filled'), 0),
		(SELECT count(*) FROM chain_blocks WHERE chain = $1 AND status <> 'skipped' AND parsed_version IS NULL),
		(SELECT count(*) FROM trades WHERE chain = $1 AND block_time > $2::timestamptz - interval '24 hours' AND block_time <= $2),
		(SELECT count(*) FROM trades WHERE chain = $1 AND value_usd IS NULL AND block_time > $2::timestamptz - interval '24 hours' AND block_time <= $2)`,
		chain, now).Scan(&b.ForwardLag, &b.GapBlocks, &b.Unparsed, &b.Trades24h, &b.Unpriced24h)
	if err != nil {
		return Backlog{}, fmt.Errorf("store: backlog: %w", err)
	}
	return b, nil
}
