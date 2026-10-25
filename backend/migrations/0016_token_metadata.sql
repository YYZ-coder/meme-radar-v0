-- H6: enrich-owned display metadata; never update parse's tokens rows.
CREATE TABLE token_metadata (
    chain           chain_id NOT NULL,
    address         bytea NOT NULL,
    source_uri      text,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','ok','failed','skipped')),
    image_url       text CHECK (char_length(image_url) <= 256),
    socials         jsonb,
    description     text CHECK (char_length(description) <= 1000),
    raw             jsonb,
    attempts        integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    next_attempt_at timestamptz,
    fetched_at      timestamptz,
    error           text,
    PRIMARY KEY (chain,address)
);
CREATE INDEX token_metadata_due ON token_metadata(chain,next_attempt_at,address)
    WHERE status IN ('pending','failed') AND attempts < 5;

-- PostgreSQL permits CREATE OR REPLACE VIEW only with new columns appended.
-- Keep 0010's token and supply columns in exactly the same order.
CREATE OR REPLACE VIEW token_info AS
SELECT t.*,
    coalesce(t.total_supply,s.total_supply) AS supply,
    coalesce(t.decimals,s.decimals) AS supply_decimals,
    CASE WHEN t.total_supply IS NOT NULL THEN 'creation_event' WHEN s.total_supply IS NOT NULL THEN 'rpc' END AS supply_source,
    m.image_url,m.socials
FROM tokens t
LEFT JOIN token_supplements s ON s.chain=t.chain AND s.address=t.address
LEFT JOIN token_metadata m ON m.chain=t.chain AND m.address=t.address;
