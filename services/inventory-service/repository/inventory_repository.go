package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNoRowsAffected = errors.New("no rows affected")

type Stock struct {
	ProductID string
	Available int32
	Reserved  int32
}

type InventoryRepository struct {
	pool *pgxpool.Pool
}

func NewInventoryRepository(dbURL string) (*InventoryRepository, error) {
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		return nil, err
	}
	return &InventoryRepository{pool: pool}, nil
}

func (r *InventoryRepository) GetStock(ctx context.Context, productID string) (*Stock, error) {
	var s Stock
	err := r.pool.QueryRow(ctx, `
		SELECT product_id, available, reserved FROM inventory WHERE product_id = $1
	`, productID).Scan(&s.ProductID, &s.Available, &s.Reserved)

	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *InventoryRepository) UpdateStock(ctx context.Context, productID string, quantity int32) (*Stock, error) {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO inventory (product_id, available) VALUES ($1, $2)
		ON CONFLICT (product_id) DO UPDATE SET available = $2, updated_at = NOW()
	`, productID, quantity)

	if err != nil {
		return nil, err
	}
	return r.GetStock(ctx, productID)
}

func (r *InventoryRepository) Reserve(ctx context.Context, productID string, quantity int32) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE inventory 
		SET available = available - $2, reserved = reserved + $2, updated_at = NOW()
		WHERE product_id = $1 AND available >= $2
	`, productID, quantity)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNoRowsAffected
	}
	return nil
}

func (r *InventoryRepository) Release(ctx context.Context, productID string, quantity int32) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE inventory 
		SET available = available + $2, reserved = reserved - $2, updated_at = NOW()
		WHERE product_id = $1
	`, productID, quantity)
	return err
}

func (r *InventoryRepository) Close() {
	r.pool.Close()
}
