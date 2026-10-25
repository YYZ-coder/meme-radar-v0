-- Parse layer (06-parse): facts decoded from raw_blocks. Market columns of
-- tokens and pools (prices, liquidity in USD, holders) arrive with compute.
ALTER TABLE chain_blocks
    ADD COLUMN parsed_version integer,
    ADD COLUMN parsed_at      timestamptz,
    ADD COLUMN hit_txs        integer,
    ADD COLUMN events         integer;
-- Blocks waiting for the parser, newest first.
CREATE INDEX chain_blocks_unparsed ON chain_blocks (chain, block_num DESC)
    WHERE status <> 'skipped' AND parsed_version IS NULL;

CREATE TABLE tokens (
    chain           chain_id    NOT NULL,
    address         bytea       NOT NULL,
    symbol          text        NOT NULL DEFAULT '',
    name            text        NOT NULL DEFAULT '',
    decimals        smallint,
    total_supply    numeric(78,0),
    creator         bytea,
    launch_platform text,
    launch_at       timestamptz,
    created_block   bigint,
    metadata_uri    text,
    metadata_status text        NOT NULL DEFAULT 'pending' CHECK (metadata_status IN ('pending', 'ok', 'failed')),
    first_seen_at   timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    PRIMARY KEY (chain, address)
);
CREATE INDEX tokens_launch ON tokens (chain, launch_at);

CREATE TABLE pools (
    chain         chain_id    NOT NULL,
    pool_id       bytea       NOT NULL,
    token_address bytea       NOT NULL,
    quote_asset   bytea       NOT NULL,
    quote_symbol  text        NOT NULL DEFAULT '',
    protocol      text        NOT NULL,
    dex           text        NOT NULL,
    status        text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'graduated', 'closed')),
    graduated_to  bytea,
    created_at    timestamptz,
    created_block bigint,
    reserve_token numeric(78,0),
    reserve_quote numeric(78,0),
    -- Position of the reserves: (state_block, state_tx, state_ix).
    state_block   bigint,
    state_tx      integer,
    state_ix      integer,
    state_at      timestamptz,
    first_seen_at timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    PRIMARY KEY (chain, pool_id)
);
CREATE INDEX pools_token ON pools (chain, token_address);

CREATE TABLE trades (
    block_time      timestamptz   NOT NULL,
    chain           chain_id      NOT NULL,
    block_num       bigint        NOT NULL,
    tx_index        integer       NOT NULL,
    ix_index        integer       NOT NULL,
    tx_hash         bytea         NOT NULL,
    pool_id         bytea         NOT NULL,
    token_address   bytea         NOT NULL,
    wallet          bytea,
    attribution     text          NOT NULL CHECK (attribution IN ('confirmed', 'inferred', 'ambiguous')),
    side            text          NOT NULL CHECK (side IN ('buy', 'sell')),
    token_amount    numeric(78,0) NOT NULL,
    quote_amount    numeric(78,0) NOT NULL,
    quote_symbol    text          NOT NULL,
    price_native    numeric,
    price_usd       numeric,
    value_usd       numeric,
    value_native    numeric,
    confirmed       boolean       NOT NULL,
    decoder         text          NOT NULL,
    decoder_version integer       NOT NULL,
    PRIMARY KEY (block_time, chain, block_num, tx_index, ix_index)
) PARTITION BY RANGE (block_time);
CREATE INDEX trades_block ON trades (chain, block_num);
CREATE INDEX trades_token ON trades (chain, token_address, block_time DESC);
CREATE INDEX trades_wallet ON trades (chain, wallet, block_time DESC) WHERE wallet IS NOT NULL;
CREATE INDEX trades_unpriced ON trades (chain, block_time) WHERE value_usd IS NULL;

CREATE TABLE liquidity_events (
    block_time      timestamptz   NOT NULL,
    chain           chain_id      NOT NULL,
    block_num       bigint        NOT NULL,
    tx_index        integer       NOT NULL,
    ix_index        integer       NOT NULL,
    tx_hash         bytea         NOT NULL,
    pool_id         bytea         NOT NULL,
    token_address   bytea         NOT NULL,
    wallet          bytea,
    kind            text          NOT NULL CHECK (kind IN ('add', 'remove')),
    token_amount    numeric(78,0) NOT NULL,
    quote_amount    numeric(78,0),
    quote_symbol    text          NOT NULL,
    value_usd       numeric,
    value_native    numeric,
    confirmed       boolean       NOT NULL,
    decoder         text          NOT NULL,
    decoder_version integer       NOT NULL,
    PRIMARY KEY (block_time, chain, block_num, tx_index, ix_index)
) PARTITION BY RANGE (block_time);
CREATE INDEX liquidity_events_block ON liquidity_events (chain, block_num);
CREATE INDEX liquidity_events_pool ON liquidity_events (chain, pool_id, block_time DESC);

CREATE TABLE decode_failures (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain      chain_id    NOT NULL,
    block_num  bigint      NOT NULL,
    tx_index   integer     NOT NULL, -- -1: the block payload itself
    tx_hash    bytea,
    decoder    text        NOT NULL,
    error      text        NOT NULL,
    created_at timestamptz NOT NULL
);
CREATE INDEX decode_failures_block ON decode_failures (chain, block_num);
