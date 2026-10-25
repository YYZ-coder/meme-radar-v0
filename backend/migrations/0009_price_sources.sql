-- Price sources are chosen in the config file (D147); allow every
-- implemented one so switching to Pyth needs no schema change.
ALTER TABLE quote_prices DROP CONSTRAINT quote_prices_source_check;
ALTER TABLE quote_prices ADD CONSTRAINT quote_prices_source_check CHECK (source IN ('binance', 'coinbase', 'pyth'));
