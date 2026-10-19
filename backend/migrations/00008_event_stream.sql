-- +goose Up
-- Unified event stream / outbox (D100–D102). Hourly partitions managed by publisher ops.

CREATE TABLE event_stream (
    id               BIGINT GENERATED ALWAYS AS IDENTITY,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    seq              BIGINT,
    stream_epoch     TEXT        NOT NULL,
    event_type       TEXT        NOT NULL,
    chain            chain_id,
    entity_id        TEXT        NOT NULL,
    entity_revision  BIGINT      NOT NULL,
    is_backfill      BOOLEAN     NOT NULL DEFAULT false,
    payload          JSONB       NOT NULL,
    PRIMARY KEY (created_at, id)
) PARTITION BY RANGE (created_at);

CREATE TABLE event_stream_default PARTITION OF event_stream DEFAULT;

CREATE INDEX event_stream_seq ON event_stream (seq)
    WHERE seq IS NOT NULL;
CREATE INDEX event_stream_unpublished ON event_stream (id)
    WHERE seq IS NULL;

-- +goose Down
DROP TABLE IF EXISTS event_stream;
