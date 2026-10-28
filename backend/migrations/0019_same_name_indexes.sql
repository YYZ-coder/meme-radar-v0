-- Cross-chain equal-symbol lookup and deterministic NULL-last filling.
-- Parse and compute retain separate tables and row ownership (D148).
CREATE INDEX tokens_equal_symbol ON tokens (lower(symbol), chain, address);

-- Common groups, including unknown/empty symbols, can take the newest ten
-- matches from market order without sorting every matching token.
CREATE INDEX token_market_recent_trade ON token_market
    (last_trade_at DESC NULLS LAST, chain, address)
    WHERE last_trade_at IS NOT NULL;
