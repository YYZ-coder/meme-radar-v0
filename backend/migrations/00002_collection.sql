-- +goose Up
-- Collection layer: anchors, raw block metadata, range jobs, gaps, reorgs (D66, D71, D73, D74, D81, D83).

CREATE TABLE chain_anchors (
    chain                  chain_id    PRIMARY KEY,
    anchor_position        BIGINT      NOT NULL,
    anchor_hash            BYTEA       NOT NULL,
    anchor_time            TIMESTAMPTZ NOT NULL,
    history_start_position BIGINT      NOT NULL,
    history_start_time     TIMESTAMPTZ NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE raw_blocks (
    chain            chain_id    NOT NULL,
    position         BIGINT      NOT NULL,
    block_hash       BYTEA       NOT NULL,
    block_height     BIGINT,
    parent_hash      BYTEA       NOT NULL,
    block_time       TIMESTAMPTZ,
    block_time_ms    BIGINT,
    tx_count         INT         NOT NULL,
    object_key       TEXT        NOT NULL,
    compressed_bytes BIGINT      NOT NULL,
    confirmation     TEXT        NOT NULL CHECK (confirmation IN ('pending', 'confirmed')),
    branch           TEXT        NOT NULL DEFAULT 'canonical'
                     CHECK (branch IN ('canonical', 'orphaned')),
    completeness     TEXT        NOT NULL DEFAULT 'complete'
                     CHECK (completeness IN ('complete', 'partial')),
    fetched_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, position, block_hash)
) PARTITION BY LIST (chain);

SELECT create_chain_range_partitions('raw_blocks', 'position');

CREATE UNIQUE INDEX raw_blocks_canonical_position
    ON raw_blocks (chain, position)
    WHERE branch = 'canonical';
CREATE INDEX raw_blocks_block_time ON raw_blocks (chain, block_time);
CREATE INDEX raw_blocks_pending ON raw_blocks (chain, confirmation)
    WHERE confirmation = 'pending';

CREATE TABLE block_jobs (
    chain            chain_id    NOT NULL,
    task_kind        TEXT        NOT NULL CHECK (task_kind IN ('forward', 'history')),
    range_start      BIGINT      NOT NULL,
    range_end        BIGINT      NOT NULL,
    status           TEXT        NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'running', 'done', 'failed')),
    done_count       INT         NOT NULL DEFAULT 0,
    skipped_count    INT         NOT NULL DEFAULT 0,
    gap_count        INT         NOT NULL DEFAULT 0,
    attempts         INT         NOT NULL DEFAULT 0,
    next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until      TIMESTAMPTZ,
    worker_id        TEXT,
    last_error_class TEXT
                     CHECK (last_error_class IN (
                         'retryable', 'config', 'not_found',
                         'history_unavailable', 'incomplete'
                     )),
    last_error       TEXT,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, task_kind, range_start),
    CHECK (range_end > range_start)
);

CREATE INDEX block_jobs_ready
    ON block_jobs (chain, task_kind, next_attempt_at)
    WHERE status = 'pending';

CREATE TABLE collection_progress (
    chain               chain_id NOT NULL,
    task_kind           TEXT     NOT NULL,
    run_id              BIGINT   NOT NULL,
    range_start         BIGINT   NOT NULL,
    range_end           BIGINT,
    continuous_position BIGINT   NOT NULL,
    continuous_hash     BYTEA,
    generation          BIGINT   NOT NULL DEFAULT 0,
    status              TEXT     NOT NULL CHECK (status IN ('running', 'paused', 'done')),
    last_error          TEXT,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, task_kind, run_id)
);

CREATE TABLE collection_gaps (
    chain       chain_id NOT NULL,
    position    BIGINT   NOT NULL,
    reason      TEXT     NOT NULL
                CHECK (reason IN ('retry_exhausted', 'history_unavailable', 'budget_paused')),
    first_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    PRIMARY KEY (chain, position)
);

CREATE TABLE reorg_events (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain           chain_id    NOT NULL,
    common_ancestor BIGINT      NOT NULL,
    old_tip         BIGINT      NOT NULL,
    new_tip         BIGINT      NOT NULL,
    detected_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS reorg_events;
DROP TABLE IF EXISTS collection_gaps;
DROP TABLE IF EXISTS collection_progress;
DROP TABLE IF EXISTS block_jobs;
DROP TABLE IF EXISTS raw_blocks;
DROP TABLE IF EXISTS chain_anchors;
