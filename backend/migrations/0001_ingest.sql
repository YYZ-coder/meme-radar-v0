-- Ingest layer (D142, D143): full raw blocks, the per-block ledger, the
-- forward cursor and gaps. Decoded facts arrive with the parsing module.
CREATE TYPE chain_id AS ENUM ('solana', 'base', 'bsc');

-- zstd-compressed full blocks, daily partitions created by
-- store.EnsurePartitions and dropped after the retention period.
CREATE TABLE raw_blocks (
    block_time  timestamptz NOT NULL,
    chain       chain_id    NOT NULL,
    block_num   bigint      NOT NULL,
    block_hash  bytea       NOT NULL,
    payload     bytea       NOT NULL,
    source      text        NOT NULL,
    received_at timestamptz NOT NULL,
    PRIMARY KEY (block_time, chain, block_num)
) PARTITION BY RANGE (block_time);
CREATE INDEX raw_blocks_chain_num ON raw_blocks (chain, block_num);

-- One row per processed block; a skipped Solana slot has no hash or time.
CREATE TABLE chain_blocks (
    chain       chain_id    NOT NULL,
    block_num   bigint      NOT NULL,
    block_hash  bytea,
    parent_hash bytea,
    block_time  timestamptz,
    status      text        NOT NULL CHECK (status IN ('pending', 'confirmed', 'skipped')),
    tx_count    integer     NOT NULL DEFAULT 0,
    ingested_at timestamptz NOT NULL,
    PRIMARY KEY (chain, block_num),
    CHECK ((status = 'skipped') = (block_hash IS NULL)),
    CHECK ((status = 'skipped') = (block_time IS NULL))
);
CREATE INDEX chain_blocks_time ON chain_blocks (chain, block_time);

CREATE TABLE stream_cursors (
    chain           chain_id    NOT NULL,
    stream          text        NOT NULL CHECK (stream IN ('forward', 'backfill')),
    anchor          bigint      NOT NULL,
    position        bigint      NOT NULL,
    head            bigint      NOT NULL,
    last_block_time timestamptz,
    last_error_kind text,
    last_error      text,
    last_error_at   timestamptz,
    updated_at      timestamptz NOT NULL,
    PRIMARY KEY (chain, stream)
);

-- Block ranges not yet processed. Filling happens top-down: to_pos shrinks
-- until the from_pos block is filled and the gap becomes 'filled'.
CREATE TABLE stream_gaps (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain      chain_id    NOT NULL,
    from_pos   bigint      NOT NULL,
    to_pos     bigint      NOT NULL,
    reason     text        NOT NULL CHECK (reason IN ('startup', 'rate_limited', 'fetch_failed', 'disconnect')),
    status     text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'filling', 'filled')),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (to_pos >= from_pos)
);
CREATE INDEX stream_gaps_open ON stream_gaps (chain, to_pos DESC) WHERE status <> 'filled';
