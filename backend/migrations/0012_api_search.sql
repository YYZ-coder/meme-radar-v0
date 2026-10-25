-- H2-4: literal case-insensitive symbol/name prefix searches.
CREATE INDEX tokens_symbol_prefix ON tokens (chain, lower(symbol) text_pattern_ops);
CREATE INDEX tokens_name_prefix ON tokens (lower(name) text_pattern_ops);
