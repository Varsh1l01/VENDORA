// Package repository persists the product catalog.
package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/db"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/outbox"
	"github.com/Varsh1l01/VENDORA/pkg/pagination"
)

// Product is a catalog entry.
type Product struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	PriceCents  int64     `json:"price_cents"`
	Currency    string    `json:"currency"`
	ImageURL    string    `json:"image_url"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Patch holds optional updates; nil fields are unchanged.
type Patch struct {
	Name        *string
	Description *string
	PriceCents  *int64
	ImageURL    *string
	IsActive    *bool
}

// Postgres implements the product store.
type Postgres struct{ pool *pgxpool.Pool }

// New returns a Postgres store.
func New(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const cols = `id, name, description, price_cents, currency, image_url, is_active, created_at, updated_at`

func scan(row pgx.Row) (*Product, error) {
	var p Product
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.PriceCents, &p.Currency, &p.ImageURL, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Create inserts p and enqueues ev atomically.
func (r *Postgres) Create(ctx context.Context, p *Product, ev events.Envelope) error {
	return db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO products (id, name, description, price_cents, currency, image_url, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
			p.ID, p.Name, p.Description, p.PriceCents, p.Currency, p.ImageURL, p.IsActive, p.CreatedAt)
		if err != nil {
			if db.IsCheckViolation(err) {
				return apperr.Invalid("product fields are out of range")
			}
			return apperr.Internal(err, "insert product")
		}
		if err := outbox.Enqueue(ctx, tx, events.TopicProduct, ev); err != nil {
			return apperr.Internal(err, "enqueue product.created")
		}
		return nil
	})
}

// Get returns a product by id.
func (r *Postgres) Get(ctx context.Context, id string) (*Product, error) {
	p, err := scan(r.pool.QueryRow(ctx, `SELECT `+cols+` FROM products WHERE id = $1`, id))
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("product not found")
	}
	if err != nil {
		return nil, apperr.Internal(err, "get product")
	}
	return p, nil
}

// BatchGet returns the products with the given ids that exist.
func (r *Postgres) BatchGet(ctx context.Context, ids []string) ([]*Product, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+cols+` FROM products WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, apperr.Internal(err, "batch get products")
	}
	return collect(rows)
}

// ListActive returns up to limit active products after cursor (newest first).
func (r *Postgres) ListActive(ctx context.Context, limit int32, after *pagination.Cursor) ([]*Product, error) {
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = r.pool.Query(ctx, `SELECT `+cols+` FROM products WHERE is_active
			ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	} else {
		rows, err = r.pool.Query(ctx, `SELECT `+cols+` FROM products WHERE is_active
			AND (created_at, id) < ($2, $3::uuid)
			ORDER BY created_at DESC, id DESC LIMIT $1`, limit, after.CreatedAt, after.ID)
	}
	if err != nil {
		return nil, apperr.Internal(err, "list products")
	}
	return collect(rows)
}

func collect(rows pgx.Rows) ([]*Product, error) {
	defer rows.Close()
	var out []*Product
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, apperr.Internal(err, "scan product")
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err, "iterate products")
	}
	return out, nil
}

// Update applies patch and returns the updated product.
func (r *Postgres) Update(ctx context.Context, id string, p Patch) (*Product, error) {
	prod, err := scan(r.pool.QueryRow(ctx, `
		UPDATE products SET
			name        = COALESCE($2, name),
			description = COALESCE($3, description),
			price_cents = COALESCE($4, price_cents),
			image_url   = COALESCE($5, image_url),
			is_active   = COALESCE($6, is_active),
			updated_at  = now()
		WHERE id = $1
		RETURNING `+cols, id, p.Name, p.Description, p.PriceCents, p.ImageURL, p.IsActive))
	switch {
	case db.IsNoRows(err):
		return nil, apperr.NotFound("product not found")
	case db.IsCheckViolation(err):
		return nil, apperr.Invalid("product fields are out of range")
	case err != nil:
		return nil, apperr.Internal(err, "update product")
	}
	return prod, nil
}

// Deactivate soft-deletes a product.
func (r *Postgres) Deactivate(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE products SET is_active = false, updated_at = now() WHERE id = $1`, id)
	if err != nil {
		return apperr.Internal(err, "deactivate product")
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("product not found")
	}
	return nil
}
