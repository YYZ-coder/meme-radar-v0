-- Line A (D150): token facts read from the chain for traded tokens whose
-- creation event is not in the stored blocks. Written by the enrich role
-- only; token_info merges them under the creation-event values parse writes.
CREATE TABLE token_supplements (
    chain        chain_id    NOT NULL,
    address      bytea       NOT NULL,
    total_supply numeric(78,0),
    decimals     smallint,
    source       text        NOT NULL DEFAULT 'rpc',
    error        text,
    fetched_at   timestamptz NOT NULL,
    PRIMARY KEY (chain, address)
);
CREATE VIEW token_info AS
SELECT t.*,
    coalesce(t.total_supply, s.total_supply) AS supply,
    coalesce(t.decimals, s.decimals) AS supply_decimals,
    CASE WHEN t.total_supply IS NOT NULL THEN 'creation_event' WHEN s.total_supply IS NOT NULL THEN 'rpc' END AS supply_source
FROM tokens t LEFT JOIN token_supplements s ON s.chain = t.chain AND s.address = t.address;
