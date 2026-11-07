CREATE TABLE IF NOT EXISTS orders (
    id               UUID PRIMARY KEY,
    user_id          UUID NOT NULL,
    status           TEXT NOT NULL CHECK (status IN
                       ('PENDING', 'AWAITING_PAYMENT', 'CONFIRMED', 'SHIPPED', 'DELIVERED', 'CANCELLED', 'REJECTED')),
    total_cents      BIGINT NOT NULL CHECK (total_cents >= 0),
    currency         CHAR(3) NOT NULL DEFAULT 'USD',
    shipping_address TEXT NOT NULL DEFAULT '',
    status_reason    TEXT NOT NULL DEFAULT '',
    idempotency_key  TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Retried checkouts with the same key resolve to the same order.
CREATE UNIQUE INDEX IF NOT EXISTS orders_idempotency_key ON orders (user_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
-- Serves ListOrders: WHERE user_id ORDER BY created_at DESC, id DESC (keyset).
CREATE INDEX IF NOT EXISTS orders_user_listing_idx ON orders (user_id, created_at DESC, id DESC);
-- Serves the saga timeout sweeper.
CREATE INDEX IF NOT EXISTS orders_open_idx ON orders (updated_at) WHERE status IN ('PENDING', 'AWAITING_PAYMENT');

CREATE TABLE IF NOT EXISTS order_items (
    order_id         UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    product_id       UUID NOT NULL,
    product_name     TEXT NOT NULL,
    unit_price_cents BIGINT NOT NULL CHECK (unit_price_cents >= 0),
    quantity         INT NOT NULL CHECK (quantity BETWEEN 1 AND 99),
    PRIMARY KEY (order_id, product_id)
);

CREATE TABLE IF NOT EXISTS order_status_history (
    id          BIGSERIAL PRIMARY KEY,
    order_id    UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    from_status TEXT,
    to_status   TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    actor       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS order_status_history_order_idx ON order_status_history (order_id, id);
