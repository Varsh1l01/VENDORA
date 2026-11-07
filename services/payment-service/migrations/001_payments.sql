CREATE TABLE IF NOT EXISTS payments (
    id                 UUID PRIMARY KEY,
    -- One payment per order: retries and duplicate requests converge on it.
    order_id           UUID NOT NULL UNIQUE,
    user_id            UUID NOT NULL,
    amount_cents       BIGINT NOT NULL CHECK (amount_cents >= 0),
    currency           CHAR(3) NOT NULL DEFAULT 'USD',
    status             TEXT NOT NULL CHECK (status IN ('PENDING', 'SUCCEEDED', 'FAILED', 'REFUNDED')),
    failure_reason     TEXT NOT NULL DEFAULT '',
    provider_reference TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS payments_user_idx ON payments (user_id, created_at DESC);
