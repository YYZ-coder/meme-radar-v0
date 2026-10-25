-- compute (docs/engineering/07-compute.md): native-coin USD prices per minute.
-- Stablecoins are 1 USD and have no rows; price sources per D147.
CREATE TABLE quote_prices (
    symbol     text        NOT NULL,
    minute     timestamptz NOT NULL,
    price_usd  numeric     NOT NULL CHECK (price_usd > 0),
    source     text        NOT NULL CHECK (source IN ('binance', 'coinbase')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (symbol, minute)
);
CREATE INDEX liquidity_events_unpriced ON liquidity_events (chain, block_time) WHERE value_usd IS NULL;

-- Quote assets compute can price: native coins (and their wrapped tokens) from
-- quote_prices, stablecoins at 1 USD. Written by compute from the chain config.
CREATE TABLE quote_assets (
    chain    chain_id NOT NULL,
    asset    bytea    NOT NULL,
    symbol   text     NOT NULL,
    kind     text     NOT NULL CHECK (kind IN ('native', 'stable')),
    decimals smallint NOT NULL CHECK (decimals BETWEEN 0 AND 36),
    PRIMARY KEY (chain, asset)
);

-- Pool liquidity in quote-asset units (both sides: reserve_quote x 2, D55),
-- sampled at the minute of each reserve change; kept 1 day for the 1-hour
-- time-weighted average that picks the main pool.
CREATE TABLE pool_liquidity (
    chain           chain_id    NOT NULL,
    pool_id         bytea       NOT NULL,
    minute          timestamptz NOT NULL,
    liquidity_quote numeric     NOT NULL,
    PRIMARY KEY (chain, pool_id, minute)
);
ALTER TABLE pools
    ADD COLUMN liquidity_quote      numeric,
    ADD COLUMN liquidity_state_at   timestamptz,
    ADD COLUMN liquidity_usd        numeric,
    ADD COLUMN liquidity_native     numeric,
    ADD COLUMN liquidity_1h_avg_usd numeric,
    ADD COLUMN liquidity_at         timestamptz;

-- Current market state of a token (02-data-model §3), kept by compute.
ALTER TABLE tokens
    ADD COLUMN main_pool_id         bytea,
    ADD COLUMN main_pool_since      timestamptz,
    ADD COLUMN main_candidate       bytea,
    ADD COLUMN main_candidate_since timestamptz,
    ADD COLUMN price_usd            numeric,
    ADD COLUMN price_native         numeric,
    ADD COLUMN market_cap_usd       numeric,
    ADD COLUMN market_cap_native    numeric,
    ADD COLUMN liquidity_usd        numeric,
    ADD COLUMN liquidity_native     numeric,
    ADD COLUMN last_trade_at        timestamptz,
    ADD COLUMN market_state_at      timestamptz;

CREATE TABLE pool_switches (
    chain         chain_id    NOT NULL,
    token_address bytea       NOT NULL,
    t             timestamptz NOT NULL,
    from_pool     bytea,
    to_pool       bytea       NOT NULL,
    reason        text        NOT NULL CHECK (reason IN ('liquidity', 'graduation')),
    PRIMARY KEY (chain, token_address, t)
);

-- Per pool and minute (02-data-model §5); retained 3 days.
CREATE TABLE pool_minute_stats (
    chain              chain_id    NOT NULL,
    pool_id            bytea       NOT NULL,
    minute             timestamptz NOT NULL,
    token_address      bytea       NOT NULL,
    dex                text        NOT NULL,
    trades             integer     NOT NULL,
    buys               integer     NOT NULL,
    sells              integer     NOT NULL,
    volume_usd         numeric,
    buy_volume_usd     numeric,
    sell_volume_usd    numeric,
    volume_native      numeric,
    buy_volume_native  numeric,
    sell_volume_native numeric,
    unpriced_trades    integer     NOT NULL,
    PRIMARY KEY (chain, pool_id, minute)
) PARTITION BY RANGE (minute);
CREATE INDEX pool_minute_stats_token ON pool_minute_stats (chain, token_address, minute);
CREATE INDEX pool_minute_stats_dex ON pool_minute_stats (chain, dex, minute);

-- compute progress: the latest chain_blocks.parsed_at whose minutes are in
-- pool_minute_stats, and minutes whose trades were priced since.
CREATE TABLE compute_marks (
    chain chain_id    NOT NULL,
    name  text        NOT NULL,
    at    timestamptz NOT NULL,
    PRIMARY KEY (chain, name)
);
CREATE TABLE stats_dirty (
    chain  chain_id    NOT NULL,
    minute timestamptz NOT NULL,
    PRIMARY KEY (chain, minute)
);

-- Token candles from main-pool trades (02-data-model §5): 1m from trades,
-- 5m/1h/1d rolled up from 1m; v is the token's USD volume over all pools.
CREATE TABLE candles (
    chain         chain_id    NOT NULL,
    token_address bytea       NOT NULL,
    interval      text        NOT NULL CHECK (interval IN ('1m', '5m', '1h', '1d')),
    t             timestamptz NOT NULL,
    pool_id       bytea       NOT NULL,
    o             numeric     NOT NULL,
    h             numeric     NOT NULL,
    l             numeric     NOT NULL,
    c             numeric     NOT NULL,
    o_native      numeric,
    h_native      numeric,
    l_native      numeric,
    c_native      numeric,
    v             numeric,
    closed        boolean     NOT NULL,
    PRIMARY KEY (chain, token_address, interval, t)
);

-- Contiguous processed block ranges (D140): parsed blocks and skipped slots.
CREATE TABLE coverage_ranges (
    chain      chain_id NOT NULL,
    from_block bigint   NOT NULL,
    to_block   bigint   NOT NULL,
    from_time  timestamptz,
    to_time    timestamptz,
    PRIMARY KEY (chain, from_block)
);
