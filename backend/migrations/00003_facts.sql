-- +goose Up
-- Fact layer: position-keyed trades, actions, balances, native transfers (D78–D80, D84, D85).

CREATE TABLE chain_txs (
    chain       chain_id      NOT NULL,
    block_num   BIGINT        NOT NULL,
    tx_index    INT           NOT NULL,
    tx_hash     BYTEA         NOT NULL,
    signer      BYTEA         NOT NULL,
    success     BOOLEAN       NOT NULL,
    fee_native  NUMERIC(78,0) NOT NULL,
    tip_native  NUMERIC(78,0),
    block_time  TIMESTAMPTZ   NOT NULL,
    PRIMARY KEY (chain, block_num, tx_index)
) PARTITION BY LIST (chain);

SELECT create_chain_range_partitions('chain_txs', 'block_num');
CREATE INDEX chain_txs_hash ON chain_txs (chain, tx_hash);

CREATE TABLE pool_trades (
    chain          chain_id      NOT NULL,
    block_num      BIGINT        NOT NULL,
    tx_index       INT           NOT NULL,
    item_index     INT           NOT NULL,
    block_time     TIMESTAMPTZ   NOT NULL,
    protocol       TEXT          NOT NULL,
    pool           BYTEA         NOT NULL,
    token          BYTEA         NOT NULL,
    quote_asset    BYTEA         NOT NULL,
    side           TEXT          NOT NULL CHECK (side IN ('buy', 'sell')),
    token_amount   NUMERIC(78,0) NOT NULL,
    quote_amount   NUMERIC(78,0) NOT NULL,
    trader         BYTEA,
    parser_version INT           NOT NULL,
    PRIMARY KEY (chain, block_num, tx_index, item_index)
) PARTITION BY LIST (chain);

SELECT create_chain_range_partitions('pool_trades', 'block_num');
CREATE INDEX pool_trades_token ON pool_trades (chain, token, block_num DESC);
CREATE INDEX pool_trades_pool ON pool_trades (chain, pool, block_num DESC);

CREATE TABLE trade_fees (
    chain      chain_id      NOT NULL,
    block_num  BIGINT        NOT NULL,
    tx_index   INT           NOT NULL,
    item_index INT           NOT NULL,
    seq        SMALLINT      NOT NULL,
    kind       TEXT          NOT NULL
               CHECK (kind IN ('protocol', 'creator', 'lp', 'aggregator', 'unknown')),
    recipient  BYTEA,
    asset      BYTEA         NOT NULL,
    amount     NUMERIC(78,0) NOT NULL,
    PRIMARY KEY (chain, block_num, tx_index, item_index, seq)
) PARTITION BY LIST (chain);

SELECT create_chain_range_partitions('trade_fees', 'block_num');

CREATE TABLE economic_actions (
    chain          chain_id      NOT NULL,
    block_num      BIGINT        NOT NULL,
    tx_index       INT           NOT NULL,
    item_index     INT           NOT NULL,
    block_time     TIMESTAMPTZ   NOT NULL,
    wallet         BYTEA         NOT NULL,
    token          BYTEA         NOT NULL,
    side           TEXT          NOT NULL CHECK (side IN ('buy', 'sell')),
    token_amount   NUMERIC(78,0) NOT NULL,
    quote_asset    BYTEA         NOT NULL,
    quote_amount   NUMERIC(78,0) NOT NULL,
    attribution    TEXT          NOT NULL
                   CHECK (attribution IN ('confirmed', 'inferred', 'ambiguous')),
    hops           SMALLINT      NOT NULL DEFAULT 1,
    parser_version INT           NOT NULL,
    PRIMARY KEY (chain, block_num, tx_index, item_index)
) PARTITION BY LIST (chain);

SELECT create_chain_range_partitions('economic_actions', 'block_num');
CREATE INDEX economic_actions_wallet ON economic_actions (chain, wallet, block_num DESC);
CREATE INDEX economic_actions_token ON economic_actions (chain, token, block_num DESC);

CREATE TABLE balance_changes (
    chain        chain_id      NOT NULL,
    block_num    BIGINT        NOT NULL,
    tx_index     INT           NOT NULL,
    item_index   INT           NOT NULL,
    owner        BYTEA         NOT NULL,
    token        BYTEA         NOT NULL,
    delta        NUMERIC(78,0) NOT NULL,
    post_balance NUMERIC(78,0),
    kind         TEXT          NOT NULL
                 CHECK (kind IN ('trade', 'transfer', 'mint', 'burn')),
    PRIMARY KEY (chain, block_num, tx_index, item_index)
) PARTITION BY LIST (chain);

SELECT create_chain_range_partitions('balance_changes', 'block_num');
CREATE INDEX balance_changes_owner_token ON balance_changes (chain, owner, token);

CREATE TABLE native_transfers (
    chain      chain_id      NOT NULL,
    block_num  BIGINT        NOT NULL,
    tx_index   INT           NOT NULL,
    item_index INT           NOT NULL,
    sender     BYTEA         NOT NULL,
    receiver   BYTEA         NOT NULL,
    amount     NUMERIC(78,0) NOT NULL,
    PRIMARY KEY (chain, block_num, tx_index, item_index)
) PARTITION BY LIST (chain);

SELECT create_chain_range_partitions('native_transfers', 'block_num');
CREATE INDEX native_transfers_receiver ON native_transfers (chain, receiver, block_num DESC);

CREATE TABLE liquidity_events (
    chain          chain_id      NOT NULL,
    block_num      BIGINT        NOT NULL,
    tx_index       INT           NOT NULL,
    item_index     INT           NOT NULL,
    block_time     TIMESTAMPTZ   NOT NULL,
    protocol       TEXT          NOT NULL,
    pool           BYTEA         NOT NULL,
    token          BYTEA         NOT NULL,
    quote_asset    BYTEA         NOT NULL,
    side           TEXT          NOT NULL CHECK (side IN ('add', 'remove')),
    token_amount   NUMERIC(78,0) NOT NULL,
    quote_amount   NUMERIC(78,0) NOT NULL,
    actor          BYTEA,
    parser_version INT           NOT NULL,
    PRIMARY KEY (chain, block_num, tx_index, item_index)
) PARTITION BY LIST (chain);

SELECT create_chain_range_partitions('liquidity_events', 'block_num');
CREATE INDEX liquidity_events_pool ON liquidity_events (chain, pool, block_num DESC);

-- +goose Down
DROP TABLE IF EXISTS liquidity_events;
DROP TABLE IF EXISTS native_transfers;
DROP TABLE IF EXISTS balance_changes;
DROP TABLE IF EXISTS economic_actions;
DROP TABLE IF EXISTS trade_fees;
DROP TABLE IF EXISTS pool_trades;
DROP TABLE IF EXISTS chain_txs;
