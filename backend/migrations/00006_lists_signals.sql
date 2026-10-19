-- +goose Up
-- Smart-money lists, labels, signals, leaderboard (D58–D60, D92–D95).

CREATE TABLE smart_money_lists (
    version        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source         TEXT     NOT NULL CHECK (source IN ('import', 'auto', 'merge')),
    rule_version   TEXT,
    status         TEXT     NOT NULL DEFAULT 'building'
                   CHECK (status IN ('building', 'active', 'retired')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    activated_at   TIMESTAMPTZ,
    retired_at     TIMESTAMPTZ
);

CREATE UNIQUE INDEX smart_money_lists_one_active
    ON smart_money_lists ((1))
    WHERE status = 'active';

CREATE TABLE smart_money_members (
    list_version   BIGINT   NOT NULL REFERENCES smart_money_lists (version),
    chain          chain_id NOT NULL,
    address        BYTEA    NOT NULL,
    display_name   TEXT,
    x_link         TEXT,
    sources        TEXT[]   NOT NULL DEFAULT '{}',
    PRIMARY KEY (list_version, chain, address)
);

CREATE TABLE smart_money_exclusions (
    chain      chain_id NOT NULL,
    address    BYTEA    NOT NULL,
    reason     TEXT     NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, address)
);

CREATE TABLE token_wallet_labels (
    chain         chain_id NOT NULL,
    token         BYTEA    NOT NULL,
    address       BYTEA    NOT NULL,
    label         TEXT     NOT NULL
                  CHECK (label IN ('dev', 'sniper', 'bundle', 'insider', 'whale')),
    rule_version  TEXT     NOT NULL,
    hit_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    evidence      JSONB,
    PRIMARY KEY (chain, token, address, label)
);

CREATE TABLE wallet_labels (
    chain         chain_id NOT NULL,
    address       BYTEA    NOT NULL,
    label         TEXT     NOT NULL CHECK (label IN ('pro_trader', 'kol')),
    rule_version  TEXT,
    import_batch  TEXT,
    hit_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    evidence      JSONB,
    PRIMARY KEY (chain, address, label)
);

CREATE TABLE signals (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain             chain_id NOT NULL,
    token             BYTEA    NOT NULL,
    side              TEXT     NOT NULL CHECK (side IN ('buy', 'sell')),
    time_window       TEXT     NOT NULL,
    rule_version      TEXT     NOT NULL,
    list_version      BIGINT   NOT NULL,
    triggered_at      TIMESTAMPTZ NOT NULL,
    trigger_block_num BIGINT   NOT NULL,
    trigger_tx_index  INT      NOT NULL,
    trigger_item_index INT     NOT NULL,
    first_mcap_usd    NUMERIC,
    wallet_count      INT      NOT NULL,
    net_buy_usd       NUMERIC  NOT NULL,
    day_seq           INT      NOT NULL,
    status            TEXT     NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active', 'reverted')),
    snapshot          JSONB    NOT NULL
);

CREATE INDEX signals_token_time ON signals (chain, token, triggered_at DESC);
CREATE INDEX signals_status_time ON signals (status, triggered_at DESC);

CREATE TABLE signal_state (
    chain           chain_id NOT NULL,
    token           BYTEA    NOT NULL,
    side            TEXT     NOT NULL CHECK (side IN ('buy', 'sell')),
    day             DATE     NOT NULL,
    last_triggered  TIMESTAMPTZ,
    day_seq         INT      NOT NULL DEFAULT 0,
    PRIMARY KEY (chain, token, side, day)
);

CREATE TABLE leaderboard_snapshots (
    chain         chain_id NOT NULL,
    period        TEXT     NOT NULL CHECK (period IN ('7d', '30d')),
    scope         TEXT     NOT NULL CHECK (scope IN ('all', 'smart', 'kol')),
    computed_at   TIMESTAMPTZ NOT NULL,
    rank          INT      NOT NULL,
    wallet        BYTEA    NOT NULL,
    pnl_usd       NUMERIC,
    pnl_pct       NUMERIC,
    win_rate      NUMERIC,
    trade_count   INT,
    buy_count     INT,
    sell_count    INT,
    native_usd    NUMERIC,
    profit_buckets INT[],
    PRIMARY KEY (chain, period, scope, computed_at, rank)
);

CREATE INDEX leaderboard_snapshots_latest
    ON leaderboard_snapshots (chain, period, scope, computed_at DESC);

-- +goose Down
DROP TABLE IF EXISTS leaderboard_snapshots;
DROP TABLE IF EXISTS signal_state;
DROP TABLE IF EXISTS signals;
DROP TABLE IF EXISTS wallet_labels;
DROP TABLE IF EXISTS token_wallet_labels;
DROP TABLE IF EXISTS smart_money_exclusions;
DROP TABLE IF EXISTS smart_money_members;
DROP TABLE IF EXISTS smart_money_lists;
