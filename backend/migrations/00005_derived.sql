-- +goose Up
-- Derived layer: prices, candles, stats, holdings, FIFO (D85, D88–D91).

CREATE TABLE quote_asset_prices (
    chain      chain_id      NOT NULL,
    asset      BYTEA         NOT NULL,
    minute     TIMESTAMPTZ   NOT NULL,
    price_usd  NUMERIC       NOT NULL,
    stale      BOOLEAN       NOT NULL DEFAULT false,
    sample_n   INT           NOT NULL DEFAULT 0,
    PRIMARY KEY (chain, asset, minute)
);

CREATE TABLE token_main_pool (
    chain           chain_id NOT NULL,
    token           BYTEA    NOT NULL,
    pool            BYTEA    NOT NULL,
    switched_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    reason          TEXT     NOT NULL,
    prev_pool       BYTEA,
    PRIMARY KEY (chain, token)
);

CREATE TABLE token_main_pool_history (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain        chain_id NOT NULL,
    token        BYTEA    NOT NULL,
    pool         BYTEA    NOT NULL,
    switched_at  TIMESTAMPTZ NOT NULL,
    reason       TEXT     NOT NULL,
    prev_pool    BYTEA
);

CREATE INDEX token_main_pool_history_token
    ON token_main_pool_history (chain, token, switched_at DESC);

CREATE TABLE candles (
    chain         chain_id NOT NULL,
    token         BYTEA    NOT NULL,
    candle_interval TEXT   NOT NULL CHECK (candle_interval IN ('1m', '5m', '1h', '1d')),
    bucket_start  TIMESTAMPTZ NOT NULL,
    open_usd      NUMERIC,
    high_usd      NUMERIC,
    low_usd       NUMERIC,
    close_usd     NUMERIC,
    volume_usd    NUMERIC  NOT NULL DEFAULT 0,
    volume_all_pools_usd NUMERIC NOT NULL DEFAULT 0,
    trade_count   INT      NOT NULL DEFAULT 0,
    PRIMARY KEY (chain, token, candle_interval, bucket_start)
);

CREATE TABLE candles_hf (
    chain         chain_id NOT NULL,
    token         BYTEA    NOT NULL,
    candle_interval TEXT   NOT NULL CHECK (candle_interval IN ('1s', '30s')),
    bucket_start  TIMESTAMPTZ NOT NULL,
    open_usd      NUMERIC,
    high_usd      NUMERIC,
    low_usd       NUMERIC,
    close_usd     NUMERIC,
    volume_usd    NUMERIC  NOT NULL DEFAULT 0,
    volume_all_pools_usd NUMERIC NOT NULL DEFAULT 0,
    trade_count   INT      NOT NULL DEFAULT 0,
    PRIMARY KEY (chain, token, candle_interval, bucket_start)
) PARTITION BY RANGE (bucket_start);

CREATE TABLE candles_hf_default PARTITION OF candles_hf DEFAULT;

CREATE TABLE compute_progress (
    job            TEXT     NOT NULL,
    chain          chain_id NOT NULL,
    continuous_position BIGINT NOT NULL DEFAULT 0,
    input_version  BIGINT   NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (job, chain)
);

CREATE TABLE token_minute_stats (
    chain          chain_id NOT NULL,
    token          BYTEA    NOT NULL,
    minute         TIMESTAMPTZ NOT NULL,
    trade_count    INT      NOT NULL DEFAULT 0,
    buy_count      INT      NOT NULL DEFAULT 0,
    sell_count     INT      NOT NULL DEFAULT 0,
    volume_usd     NUMERIC  NOT NULL DEFAULT 0,
    buy_usd        NUMERIC  NOT NULL DEFAULT 0,
    sell_usd       NUMERIC  NOT NULL DEFAULT 0,
    trader_sketch  BYTEA,
    PRIMARY KEY (chain, token, minute)
) PARTITION BY RANGE (minute);

CREATE TABLE token_minute_stats_default PARTITION OF token_minute_stats DEFAULT;
CREATE INDEX token_minute_stats_minute ON token_minute_stats (chain, minute);

CREATE TABLE sm_minute_stats (
    list_version   BIGINT   NOT NULL,
    chain          chain_id NOT NULL,
    token          BYTEA    NOT NULL,
    minute         TIMESTAMPTZ NOT NULL,
    buy_usd        NUMERIC  NOT NULL DEFAULT 0,
    sell_usd       NUMERIC  NOT NULL DEFAULT 0,
    buy_count      INT      NOT NULL DEFAULT 0,
    sell_count     INT      NOT NULL DEFAULT 0,
    wallet_count   INT      NOT NULL DEFAULT 0,
    PRIMARY KEY (list_version, chain, token, minute)
) PARTITION BY RANGE (minute);

CREATE TABLE sm_minute_stats_default PARTITION OF sm_minute_stats DEFAULT;

CREATE TABLE token_window_current (
    chain              chain_id NOT NULL,
    time_window        TEXT     NOT NULL
                       CHECK (time_window IN ('1m', '5m', '1h', '4h', '24h')),
    token              BYTEA    NOT NULL,
    price_usd          NUMERIC,
    market_cap_usd     NUMERIC,
    price_change_pct   NUMERIC,
    liquidity_usd      NUMERIC,
    trade_count        INT,
    buy_count          INT,
    sell_count         INT,
    volume_usd         NUMERIC,
    sm_holders         INT,
    sm_trades          INT,
    sm_net_inflow_usd  NUMERIC,
    holder_count       INT,
    age_seconds        BIGINT,
    dex_set            TEXT[],
    launch_platform    TEXT,
    social_flags       JSONB,
    sparkline          NUMERIC[],
    list_version       BIGINT,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, time_window, token)
);

CREATE INDEX token_window_current_sm_trades
    ON token_window_current (chain, time_window, sm_trades DESC NULLS LAST);
CREATE INDEX token_window_current_volume
    ON token_window_current (chain, time_window, volume_usd DESC NULLS LAST);
CREATE INDEX token_window_current_mcap
    ON token_window_current (chain, time_window, market_cap_usd DESC NULLS LAST);
CREATE INDEX token_window_current_net_inflow
    ON token_window_current (chain, time_window, sm_net_inflow_usd DESC NULLS LAST);

CREATE TABLE wallet_token_daily (
    chain           chain_id NOT NULL,
    wallet          BYTEA    NOT NULL,
    token           BYTEA    NOT NULL,
    day             DATE     NOT NULL,
    buy_count       INT      NOT NULL DEFAULT 0,
    sell_count      INT      NOT NULL DEFAULT 0,
    buy_usd         NUMERIC  NOT NULL DEFAULT 0,
    sell_usd        NUMERIC  NOT NULL DEFAULT 0,
    realized_usd    NUMERIC  NOT NULL DEFAULT 0,
    closed          BOOLEAN  NOT NULL DEFAULT false,
    has_sell        BOOLEAN  NOT NULL DEFAULT false,
    token_pnl       NUMERIC,
    cost_complete   BOOLEAN  NOT NULL DEFAULT true,
    attribution_ok  BOOLEAN  NOT NULL DEFAULT true,
    PRIMARY KEY (chain, wallet, token, day)
);

CREATE INDEX wallet_token_daily_wallet_day
    ON wallet_token_daily (chain, wallet, day);

CREATE TABLE token_info_cache (
    chain              chain_id NOT NULL,
    token              BYTEA    NOT NULL,
    top10_pct          NUMERIC,
    creator_pct        NUMERIC,
    insider_pct        NUMERIC,
    bundle_pct         NUMERIC,
    sniper_pct         NUMERIC,
    holder_count       INT,
    pro_trader_count   INT,
    lp_burned_pct      NUMERIC,
    computed_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, token)
);

CREATE TABLE holding_baselines (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain          chain_id NOT NULL,
    token          BYTEA    NOT NULL,
    version        INT      NOT NULL,
    source         TEXT     NOT NULL,
    baseline_block BIGINT   NOT NULL,
    baseline_hash  BYTEA,
    coverage       TEXT     NOT NULL CHECK (coverage IN ('full', 'partial', 'unknown')),
    active         BOOLEAN  NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chain, token, version)
);

CREATE TABLE holdings (
    chain          chain_id      NOT NULL,
    wallet         BYTEA         NOT NULL,
    token          BYTEA         NOT NULL,
    balance        NUMERIC(78,0) NOT NULL,
    updated_block  BIGINT        NOT NULL,
    updated_at     TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, wallet, token)
);

CREATE INDEX holdings_token ON holdings (chain, token, balance DESC);

CREATE TABLE fifo_lots (
    chain          chain_id      NOT NULL,
    wallet         BYTEA         NOT NULL,
    token          BYTEA         NOT NULL,
    opened_block   BIGINT        NOT NULL,
    opened_tx      INT           NOT NULL,
    opened_item    INT           NOT NULL,
    qty_remaining  NUMERIC(78,0) NOT NULL,
    cost_usd_remaining NUMERIC,
    cost_complete  BOOLEAN       NOT NULL DEFAULT true,
    PRIMARY KEY (chain, wallet, token, opened_block, opened_tx, opened_item)
);

CREATE TABLE realized_pnl (
    chain          chain_id NOT NULL,
    wallet         BYTEA    NOT NULL,
    token          BYTEA    NOT NULL,
    sell_block     BIGINT   NOT NULL,
    sell_tx        INT      NOT NULL,
    sell_item      INT      NOT NULL,
    qty            NUMERIC(78,0) NOT NULL,
    cost_usd       NUMERIC,
    proceeds_usd   NUMERIC,
    pnl_usd        NUMERIC,
    cost_complete  BOOLEAN  NOT NULL DEFAULT true,
    PRIMARY KEY (chain, wallet, token, sell_block, sell_tx, sell_item)
);

CREATE TABLE recompute_jobs (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job            TEXT     NOT NULL,
    chain          chain_id NOT NULL,
    range_start    BIGINT   NOT NULL,
    range_end      BIGINT   NOT NULL,
    reason         TEXT     NOT NULL,
    input_version  BIGINT   NOT NULL DEFAULT 0,
    status         TEXT     NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'running', 'done', 'failed')),
    attempts       INT      NOT NULL DEFAULT 0,
    last_error     TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX recompute_jobs_pending
    ON recompute_jobs (status, created_at)
    WHERE status = 'pending';

-- +goose Down
DROP TABLE IF EXISTS recompute_jobs;
DROP TABLE IF EXISTS realized_pnl;
DROP TABLE IF EXISTS fifo_lots;
DROP TABLE IF EXISTS holdings;
DROP TABLE IF EXISTS holding_baselines;
DROP TABLE IF EXISTS token_info_cache;
DROP TABLE IF EXISTS wallet_token_daily;
DROP TABLE IF EXISTS token_window_current;
DROP TABLE IF EXISTS sm_minute_stats;
DROP TABLE IF EXISTS token_minute_stats;
DROP TABLE IF EXISTS compute_progress;
DROP TABLE IF EXISTS candles_hf;
DROP TABLE IF EXISTS candles;
DROP TABLE IF EXISTS token_main_pool_history;
DROP TABLE IF EXISTS token_main_pool;
DROP TABLE IF EXISTS quote_asset_prices;
