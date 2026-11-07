package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Product struct {
	ID          string
	Name        string
	Description string
	Price       float64
	ImageURL    string
	Stock       int32
	IsActive    bool
	CreatedAt   time.Time
}

type ProductRepository struct {
	pool *pgxpool.Pool
}

func NewProductRepository(dbURL string) (*ProductRepository, error) {
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		return nil, err
	}
	return &ProductRepository{pool: pool}, nil
}

func (r *ProductRepository) Create(ctx context.Context, p *Product) error {
	p.ID = uuid.New().String()
	p.CreatedAt = time.Now()

	_, err := r.pool.Exec(ctx, `
		INSERT INTO products (id, name, description, price, image_url, stock, is_active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, p.ID, p.Name, p.Description, p.Price, p.ImageURL, p.Stock, p.IsActive, p.CreatedAt)

	return err
}

func (r *ProductRepository) GetByID(ctx context.Context, id string) (*Product, error) {
	var p Product
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, description, price, image_url, stock, is_active, created_at
		FROM products WHERE id = $1
	`, id).Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.ImageURL, &p.Stock, &p.IsActive, &p.CreatedAt)

	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *ProductRepository) List(ctx context.Context, page, limit int32) ([]*Product, int32, error) {
	offset := (page - 1) * limit

	// Get total count
	var total int32
	r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM products WHERE is_active = true").Scan(&total)

	// Get products
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, description, price, image_url, stock, is_active, created_at
		FROM products WHERE is_active = true
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var products []*Product
	for rows.Next() {
		var p Product
		rows.Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.ImageURL, &p.Stock, &p.IsActive, &p.CreatedAt)
		products = append(products, &p)
	}

	return products, total, nil
}

func (r *ProductRepository) Update(ctx context.Context, p *Product) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE products SET name=$2, description=$3, price=$4, image_url=$5, stock=$6, is_active=$7
		WHERE id = $1
	`, p.ID, p.Name, p.Description, p.Price, p.ImageURL, p.Stock, p.IsActive)
	return err
}

func (r *ProductRepository) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM products WHERE id = $1", id)
	return err
}

func (r *ProductRepository) Close() {
	r.pool.Close()
}
