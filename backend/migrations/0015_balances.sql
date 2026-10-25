-- H5 / D154: independent balance facts and compute-owned holdings.
CREATE TABLE balance_deltas (
    block_time timestamptz NOT NULL, chain chain_id NOT NULL, block_num bigint NOT NULL,
    tx_index integer NOT NULL, ix_index integer NOT NULL,
    token_address bytea NOT NULL, owner bytea NOT NULL, delta numeric(78,0) NOT NULL,
    PRIMARY KEY (block_time, chain, block_num, tx_index, ix_index, token_address, owner)
) PARTITION BY RANGE (block_time);
CREATE INDEX balance_deltas_block ON balance_deltas (chain, block_num);
CREATE INDEX balance_deltas_token_time ON balance_deltas (chain, token_address, block_time);
ALTER TABLE chain_blocks ADD COLUMN balance_version integer, ADD COLUMN balance_parsed_at timestamptz;
CREATE INDEX chain_blocks_balance_pending ON chain_blocks (chain, block_num DESC)
    WHERE status <> 'skipped' AND parsed_version IS NOT NULL AND balance_version IS NULL;
CREATE INDEX chain_blocks_balance_parsed ON chain_blocks (chain, balance_parsed_at) WHERE balance_parsed_at IS NOT NULL;
CREATE TABLE token_balances (
    chain chain_id NOT NULL, token_address bytea NOT NULL, owner bytea NOT NULL,
    balance numeric(78,0) NOT NULL, updated_at timestamptz NOT NULL,
    PRIMARY KEY (chain, token_address, owner)
);
CREATE INDEX token_balances_rank ON token_balances (chain, token_address, balance DESC, owner)
    WHERE balance > 0;
CREATE TABLE balance_journal (
    chain chain_id NOT NULL, block_num bigint NOT NULL, token_address bytea NOT NULL,
    owner bytea NOT NULL, delta numeric(78,0) NOT NULL,
    PRIMARY KEY (chain, block_num, token_address, owner)
);
CREATE TABLE token_holder_state (
    chain chain_id NOT NULL, token_address bytea NOT NULL, holders integer NOT NULL,
    coverage text NOT NULL CHECK (coverage IN ('full','partial','unknown')),
    since timestamptz, as_of timestamptz NOT NULL, PRIMARY KEY (chain, token_address)
);

-- Per-batch incremental tracking (first_seen_at is a chain timestamp).
CREATE INDEX tokens_balance_refresh ON tokens (chain,first_seen_at);

-- One exclusion rule for compute counts and future holder API queries.
CREATE FUNCTION is_holder_owner(p_chain chain_id,p_owner bytea) RETURNS boolean
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE WHEN p_chain='solana' THEN p_owner<>decode(repeat('00',32),'hex')
        ELSE p_owner<>decode(repeat('00',20),'hex')
            AND p_owner<>decode(repeat('00',18)||'dead','hex') END
$$;

CREATE INDEX tokens_balance_updated ON tokens (chain,updated_at);
