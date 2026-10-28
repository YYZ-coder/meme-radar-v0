-- Window start prices can precede every recent window. Skip historical
-- observations whose USD price is unknown. Keep the existing time-index key
-- order so other price readers retain their current same-timestamp behavior;
-- window SQL continues to apply its explicit transaction tie-breakers.
CREATE INDEX trades_priced_token_time ON trades
    (chain, token_address, block_time DESC)
    WHERE price_usd IS NOT NULL;
