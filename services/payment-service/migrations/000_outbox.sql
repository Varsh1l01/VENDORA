-- Transactional outbox and consumer inbox (see pkg/outbox).
CREATE TABLE IF NOT EXISTS outbox (
    id           UUID PRIMARY KEY,
    topic        TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    event_type   TEXT NOT NULL,
    envelope     JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS outbox_unpublished_idx ON outbox (created_at) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS processed_events (
    consumer     TEXT NOT NULL,
    event_id     UUID NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);
