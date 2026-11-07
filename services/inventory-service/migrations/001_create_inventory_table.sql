CREATE TABLE IF NOT EXISTS inventory (
    product_id UUID PRIMARY KEY,
    available INT DEFAULT 0,
    reserved INT DEFAULT 0,
    updated_at TIMESTAMP DEFAULT NOW()
);
