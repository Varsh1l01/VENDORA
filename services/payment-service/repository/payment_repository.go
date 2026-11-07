package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Payment struct {
	ID        string
	OrderID   string
	UserID    string
	Amount    float64
	Status    string
	CreatedAt time.Time
}

type PaymentRepository struct {
	pool *pgxpool.Pool
}

func NewPaymentRepository(dbURL string) (*PaymentRepository, error) {
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		return nil, err
	}
	return &PaymentRepository{pool: pool}, nil
}

func (r *PaymentRepository) Create(ctx context.Context, p *Payment) error {
	p.ID = uuid.New().String()
	p.CreatedAt = time.Now()
	p.Status = "pending"

	_, err := r.pool.Exec(ctx, `
		INSERT INTO payments (id, order_id, user_id, amount, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, p.ID, p.OrderID, p.UserID, p.Amount, p.Status, p.CreatedAt)

	return err
}

func (r *PaymentRepository) GetByOrderID(ctx context.Context, orderID string) (*Payment, error) {
	var p Payment
	err := r.pool.QueryRow(ctx, `
		SELECT id, order_id, user_id, amount, status, created_at
		FROM payments WHERE order_id = $1
	`, orderID).Scan(&p.ID, &p.OrderID, &p.UserID, &p.Amount, &p.Status, &p.CreatedAt)

	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PaymentRepository) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := r.pool.Exec(ctx, "UPDATE payments SET status = $2 WHERE id = $1", id, status)
	return err
}

func (r *PaymentRepository) GetByID(ctx context.Context, id string) (*Payment, error) {
	var p Payment
	err := r.pool.QueryRow(ctx, `
		SELECT id, order_id, user_id, amount, status, created_at
		FROM payments WHERE id = $1
	`, id).Scan(&p.ID, &p.OrderID, &p.UserID, &p.Amount, &p.Status, &p.CreatedAt)

	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PaymentRepository) Close() {
	r.pool.Close()
}
