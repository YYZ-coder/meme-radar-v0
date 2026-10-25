-- Cached answers of read-only chain lookups (eth_call at the latest block)
-- that never change for a token: Four.meme token info, ERC-20 metadata.
CREATE TABLE token_lookups (
    chain      chain_id    NOT NULL,
    address    bytea       NOT NULL,
    kind       text        NOT NULL CHECK (kind IN ('four_meme', 'erc20')),
    result     jsonb       NOT NULL,
    checked_at timestamptz NOT NULL,
    PRIMARY KEY (chain, address, kind)
);
