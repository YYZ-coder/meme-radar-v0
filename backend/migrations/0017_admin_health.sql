-- H7: parser-owned startup status, and API-requested retries consumed by parse.
-- Deliberately no failure FK: ApplyParsed replaces decode_failures while retry
-- history survives to count attempts on the block.
CREATE TABLE parser_status (
    chain chain_id PRIMARY KEY,
    version integer NOT NULL,
    decoders text[] NOT NULL,
    started_at timestamptz NOT NULL
);
CREATE TABLE parse_retries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain chain_id NOT NULL,
    block_num bigint NOT NULL,
    failure_id bigint NOT NULL,
    requested_at timestamptz NOT NULL,
    done_at timestamptz
);
CREATE UNIQUE INDEX parse_retries_pending ON parse_retries (failure_id) WHERE done_at IS NULL;
CREATE INDEX parse_retries_chain_pending ON parse_retries (chain, id) WHERE done_at IS NULL;
CREATE INDEX parse_retries_block_done ON parse_retries (chain, block_num) WHERE done_at IS NOT NULL;
CREATE INDEX decode_failures_recent ON decode_failures (created_at DESC, id DESC);
