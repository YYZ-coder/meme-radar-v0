-- Market state moves to compute-only tables (D148): parse keeps writing
-- tokens and pools, compute writes these, so the two roles never contend
-- for the same rows (deadlocks seen on 2026-10-23) and tokens is not
-- rewritten every pass.
CREATE TABLE pool_market (
    chain                chain_id NOT NULL,
    pool_id              bytea    NOT NULL,
    liquidity_quote      numeric,
    liquidity_state_at   timestamptz,
    liquidity_usd        numeric,
    liquidity_native     numeric,
    liquidity_1h_avg_usd numeric,
    liquidity_at         timestamptz,
    PRIMARY KEY (chain, pool_id)
);
CREATE TABLE token_market (
    chain                chain_id NOT NULL,
    address              bytea    NOT NULL,
    main_pool_id         bytea,
    main_pool_since      timestamptz,
    main_candidate       bytea,
    main_candidate_since timestamptz,
    price_usd            numeric,
    price_native         numeric,
    market_cap_usd       numeric,
    market_cap_native    numeric,
    circulating_supply   numeric, -- whole tokens; burned supply not yet known
    liquidity_usd        numeric,
    liquidity_native     numeric,
    last_trade_at        timestamptz,
    market_state_at      timestamptz,
    PRIMARY KEY (chain, address)
);
-- Tokens whose trades were priced since the last market pass.
CREATE TABLE market_dirty (
    chain         chain_id NOT NULL,
    token_address bytea    NOT NULL,
    PRIMARY KEY (chain, token_address)
);
INSERT INTO pool_market SELECT chain, pool_id, liquidity_quote, liquidity_state_at, liquidity_usd, liquidity_native, liquidity_1h_avg_usd, liquidity_at
    FROM pools WHERE liquidity_quote IS NOT NULL;
INSERT INTO token_market SELECT chain, address, main_pool_id, main_pool_since, main_candidate, main_candidate_since, price_usd, price_native,
    market_cap_usd, market_cap_native, total_supply * 10::numeric ^ (-decimals), liquidity_usd, liquidity_native, last_trade_at, market_state_at
    FROM tokens WHERE main_pool_id IS NOT NULL OR market_state_at IS NOT NULL;
ALTER TABLE pools
    DROP COLUMN liquidity_quote, DROP COLUMN liquidity_state_at, DROP COLUMN liquidity_usd,
    DROP COLUMN liquidity_native, DROP COLUMN liquidity_1h_avg_usd, DROP COLUMN liquidity_at;
ALTER TABLE tokens
    DROP COLUMN main_pool_id, DROP COLUMN main_pool_since, DROP COLUMN main_candidate, DROP COLUMN main_candidate_since,
    DROP COLUMN price_usd, DROP COLUMN price_native, DROP COLUMN market_cap_usd, DROP COLUMN market_cap_native,
    DROP COLUMN liquidity_usd, DROP COLUMN liquidity_native, DROP COLUMN last_trade_at, DROP COLUMN market_state_at;
