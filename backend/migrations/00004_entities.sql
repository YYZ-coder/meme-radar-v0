-- +goose Up
-- Entity / parse-control tables (non-partitioned).

CREATE TABLE tokens (
    chain            chain_id    NOT NULL,
    address          BYTEA       NOT NULL,
    symbol           TEXT        NOT NULL CHECK (char_length(symbol) <= 32),
    name             TEXT        NOT NULL CHECK (char_length(name) <= 128),
    raw_metadata     JSONB,
    decimals         SMALLINT    NOT NULL,
    total_supply     NUMERIC(78,0),
    creator          BYTEA,
    launch_platform  TEXT,
    launch_block_num BIGINT,
    launch_tx_index  INT,
    tracked_since    BIGINT,
    metadata_uri     TEXT,
    social           JSONB,
    first_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, address)
);

CREATE INDEX tokens_symbol_trgm ON tokens USING gin (symbol gin_trgm_ops);
CREATE INDEX tokens_name_trgm ON tokens USING gin (name gin_trgm_ops);
CREATE INDEX tokens_tracked ON tokens (chain, tracked_since)
    WHERE tracked_since IS NOT NULL;

CREATE TABLE pools (
    chain            chain_id NOT NULL,
    pool             BYTEA    NOT NULL,
    protocol         TEXT     NOT NULL,
    token            BYTEA    NOT NULL,
    quote_asset      BYTEA    NOT NULL,
    hooks            BYTEA,
    created_block_num BIGINT,
    created_tx_index INT,
    status           TEXT     NOT NULL DEFAULT 'active'
                     CHECK (status IN ('active', 'graduated', 'closed')),
    migrated_from    BYTEA,
    PRIMARY KEY (chain, pool)
);

CREATE INDEX pools_token ON pools (chain, token);

CREATE TABLE pool_migrations (
    chain              chain_id NOT NULL,
    from_pool          BYTEA    NOT NULL,
    to_pool            BYTEA    NOT NULL,
    migrate_block_num  BIGINT   NOT NULL,
    migrate_tx_index   INT      NOT NULL,
    evidence           JSONB,
    PRIMARY KEY (chain, from_pool)
);

CREATE TABLE parse_progress (
    chain          chain_id NOT NULL,
    module         TEXT     NOT NULL,
    parser_version INT      NOT NULL,
    task_kind      TEXT     NOT NULL CHECK (task_kind IN ('forward', 'history')),
    continuous_position BIGINT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, module, parser_version, task_kind)
);

CREATE TABLE parse_failures (
    chain      chain_id NOT NULL,
    module     TEXT     NOT NULL,
    block_num  BIGINT   NOT NULL,
    tx_index   INT      NOT NULL,
    error      TEXT     NOT NULL,
    attempts   INT      NOT NULL DEFAULT 0,
    status     TEXT     NOT NULL DEFAULT 'pending'
               CHECK (status IN ('pending', 'retrying', 'dead', 'resolved')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, module, block_num, tx_index)
);

CREATE TABLE parse_quarantine (
    chain          chain_id NOT NULL,
    block_num      BIGINT   NOT NULL,
    tx_index       INT      NOT NULL,
    reason         TEXT     NOT NULL,
    parser_version INT      NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, block_num, tx_index)
);

CREATE TABLE token_metadata_jobs (
    chain      chain_id NOT NULL,
    token      BYTEA    NOT NULL,
    status     TEXT     NOT NULL DEFAULT 'pending'
               CHECK (status IN ('pending', 'running', 'done', 'failed')),
    attempts   INT      NOT NULL DEFAULT 0,
    last_error TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, token)
);

CREATE TABLE attribution_tasks (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain        chain_id NOT NULL,
    block_num    BIGINT   NOT NULL,
    tx_index     INT      NOT NULL,
    status       TEXT     NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'running', 'done', 'failed')),
    attempts     INT      NOT NULL DEFAULT 0,
    last_error   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX attribution_tasks_position
    ON attribution_tasks (chain, block_num, tx_index);

CREATE TABLE attribution_revisions (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain        chain_id NOT NULL,
    block_num    BIGINT   NOT NULL,
    tx_index     INT      NOT NULL,
    item_index   INT      NOT NULL,
    old_attribution TEXT,
    new_attribution TEXT  NOT NULL
                    CHECK (new_attribution IN ('confirmed', 'inferred', 'ambiguous')),
    evidence     JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX attribution_revisions_position
    ON attribution_revisions (chain, block_num, tx_index);

-- +goose Down
DROP TABLE IF EXISTS attribution_revisions;
DROP TABLE IF EXISTS attribution_tasks;
DROP TABLE IF EXISTS token_metadata_jobs;
DROP TABLE IF EXISTS parse_quarantine;
DROP TABLE IF EXISTS parse_failures;
DROP TABLE IF EXISTS parse_progress;
DROP TABLE IF EXISTS pool_migrations;
DROP TABLE IF EXISTS pools;
DROP TABLE IF EXISTS tokens;
