-- +goose Up
-- Extensions and shared types (D77–D82).

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TYPE chain_id AS ENUM ('solana', 'base', 'bsc');

-- Helper: LIST(chain) parent → per-chain RANGE child + DEFAULT catch-all.
-- Ingest creates sized ranges ahead of tip; DEFAULT absorbs until then (D80, D83).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION create_chain_range_partitions(parent_table text, range_col text)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    c text;
BEGIN
    FOREACH c IN ARRAY ARRAY['solana', 'base', 'bsc'] LOOP
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF %I FOR VALUES IN (%L::chain_id) PARTITION BY RANGE (%I)',
            parent_table || '_' || c, parent_table, c, range_col
        );
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF %I DEFAULT',
            parent_table || '_' || c || '_default', parent_table || '_' || c
        );
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS create_chain_range_partitions(text, text);
DROP TYPE IF EXISTS chain_id;
DROP EXTENSION IF EXISTS pg_trgm;
