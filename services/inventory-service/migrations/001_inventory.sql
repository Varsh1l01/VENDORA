CREATE TABLE IF NOT EXISTS inventory (
    product_id UUID PRIMARY KEY,
    available  INT NOT NULL DEFAULT 0 CHECK (available >= 0),
    reserved   INT NOT NULL DEFAULT 0 CHECK (reserved >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per (order, product). The status transitions are
-- reserved -> committed (order paid) or reserved -> released (order cancelled),
-- which makes release/commit idempotent.
CREATE TABLE IF NOT EXISTS inventory_reservations (
    order_id   UUID NOT NULL,
    product_id UUID NOT NULL REFERENCES inventory (product_id),
    quantity   INT NOT NULL CHECK (quantity > 0),
    status     TEXT NOT NULL DEFAULT 'reserved' CHECK (status IN ('reserved', 'committed', 'released')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (order_id, product_id)
);
