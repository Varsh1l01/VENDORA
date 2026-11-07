package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OrderItem struct {
	ID          string
	OrderID     string
	ProductID   string
	ProductName string
	Price       float64
	Quantity    int32
}

type Order struct {
	ID              string
	UserID          string
	Status          string
	TotalAmount     float64
	ShippingAddress string
	Items           []OrderItem
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type OrderRepository struct {
	pool *pgxpool.Pool
}

func NewOrderRepository(dbURL string) (*OrderRepository, error) {
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		return nil, err
	}
	return &OrderRepository{pool: pool}, nil
}

func (r *OrderRepository) Create(ctx context.Context, order *Order) error {
	order.ID = uuid.New().String()
	order.CreatedAt = time.Now()
	order.UpdatedAt = time.Now()
	order.Status = "pending"

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Insert order
	_, err = tx.Exec(ctx, `
		INSERT INTO orders (id, user_id, status, total_amount, shipping_address, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, order.ID, order.UserID, order.Status, order.TotalAmount, order.ShippingAddress, order.CreatedAt, order.UpdatedAt)
	if err != nil {
		return err
	}

	// Insert order items
	for i := range order.Items {
		order.Items[i].ID = uuid.New().String()
		order.Items[i].OrderID = order.ID
		_, err = tx.Exec(ctx, `
			INSERT INTO order_items (id, order_id, product_id, product_name, price, quantity)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, order.Items[i].ID, order.ID, order.Items[i].ProductID, order.Items[i].ProductName, order.Items[i].Price, order.Items[i].Quantity)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (r *OrderRepository) GetByID(ctx context.Context, id string) (*Order, error) {
	var order Order
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, status, total_amount, shipping_address, created_at, updated_at
		FROM orders WHERE id = $1
	`, id).Scan(&order.ID, &order.UserID, &order.Status, &order.TotalAmount, &order.ShippingAddress, &order.CreatedAt, &order.UpdatedAt)
	if err != nil {
		return nil, err
	}

	// Get order items
	rows, err := r.pool.Query(ctx, `
		SELECT id, order_id, product_id, product_name, price, quantity
		FROM order_items WHERE order_id = $1
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var item OrderItem
		rows.Scan(&item.ID, &item.OrderID, &item.ProductID, &item.ProductName, &item.Price, &item.Quantity)
		order.Items = append(order.Items, item)
	}

	return &order, nil
}

func (r *OrderRepository) ListByUserID(ctx context.Context, userID string, page, limit int32) ([]*Order, int32, error) {
	offset := (page - 1) * limit

	var total int32
	r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM orders WHERE user_id = $1", userID).Scan(&total)

	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, status, total_amount, shipping_address, created_at, updated_at
		FROM orders WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var orders []*Order
	for rows.Next() {
		var order Order
		rows.Scan(&order.ID, &order.UserID, &order.Status, &order.TotalAmount, &order.ShippingAddress, &order.CreatedAt, &order.UpdatedAt)
		orders = append(orders, &order)
	}

	return orders, total, nil
}

func (r *OrderRepository) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE orders SET status = $2, updated_at = NOW() WHERE id = $1
	`, id, status)
	return err
}

func (r *OrderRepository) Close() {
	r.pool.Close()
}
