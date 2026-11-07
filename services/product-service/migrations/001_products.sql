CREATE TABLE IF NOT EXISTS products (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 5000),
    price_cents BIGINT NOT NULL CHECK (price_cents >= 0),
    currency    CHAR(3) NOT NULL DEFAULT 'USD',
    image_url   TEXT NOT NULL DEFAULT '',
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Serves the catalog listing: WHERE is_active ORDER BY created_at DESC, id DESC.
CREATE INDEX IF NOT EXISTS products_active_listing_idx ON products (created_at DESC, id DESC) WHERE is_active;
