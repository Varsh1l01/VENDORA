// Package repository persists stock levels and order reservations.
//
// Correctness relies on the database, not on application locks:
//   - stock is decremented with a conditional UPDATE (available >= qty), so
//     concurrent reservations can never oversell;
//   - CHECK constraints forbid negative available/reserved values;
//   - rows are locked in product_id order to prevent deadlocks between
//     concurrent multi-line reservations.
package repository

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/db"
	"github.com/Varsh1l01/VENDORA/pkg/outbox"
)

// Stock is a product's stock level.
type Stock struct {
	ProductID string
	Available int32
	Reserved  int32
}

// Line is a quantity of one product.
type Line struct {
	ProductID string
	Quantity  int32
}

// Postgres implements the inventory store.
type Postgres struct{ pool *pgxpool.Pool }

// New returns a Postgres store.
func New(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

// Get returns a product's stock.
func (r *Postgres) Get(ctx context.Context, productID string) (*Stock, error) {
	var s Stock
	err := r.pool.QueryRow(ctx, `SELECT product_id, available, reserved FROM inventory WHERE product_id = $1`, productID).
		Scan(&s.ProductID, &s.Available, &s.Reserved)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("stock not found for product")
	}
	if err != nil {
		return nil, apperr.Internal(err, "get stock")
	}
	return &s, nil
}

// Set sets the absolute available quantity, creating the row if needed.
func (r *Postgres) Set(ctx context.Context, productID string, available int32) (*Stock, error) {
	var s Stock
	err := r.pool.QueryRow(ctx, `
		INSERT INTO inventory (product_id, available) VALUES ($1, $2)
		ON CONFLICT (product_id) DO UPDATE SET available = EXCLUDED.available, updated_at = now()
		RETURNING product_id, available, reserved`, productID, available).Scan(&s.ProductID, &s.Available, &s.Reserved)
	if err != nil {
		return nil, apperr.Internal(err, "set stock")
	}
	return &s, nil
}

// Adjust adds delta to available. It fails if the result would be negative.
func (r *Postgres) Adjust(ctx context.Context, productID string, delta int32) (*Stock, error) {
	var s Stock
	err := r.pool.QueryRow(ctx, `
		INSERT INTO inventory (product_id, available) VALUES ($1, $2)
		ON CONFLICT (product_id) DO UPDATE SET available = inventory.available + EXCLUDED.available, updated_at = now()
		RETURNING product_id, available, reserved`, productID, delta).Scan(&s.ProductID, &s.Available, &s.Reserved)
	if db.IsCheckViolation(err) {
		return nil, apperr.FailedPrecondition("adjustment would make available stock negative")
	}
	if err != nil {
		return nil, apperr.Internal(err, "adjust stock")
	}
	return &s, nil
}

// Provision creates a stock row once per product (event consumer). It is a
// no-op when the event was already processed or the row exists.
func (r *Postgres) Provision(ctx context.Context, consumer, eventID, productID string, initial int32) error {
	return db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		first, err := outbox.MarkProcessed(ctx, tx, consumer, eventID)
		if err != nil || !first {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO inventory (product_id, available) VALUES ($1, $2) ON CONFLICT (product_id) DO NOTHING`,
			productID, initial)
		return err
	})
}

// Reserve reserves every line for orderID or nothing. Calling it again for
// the same order is a no-op (idempotent).
func (r *Postgres) Reserve(ctx context.Context, orderID string, lines []Line) error {
	lines = merge(lines)
	return db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var existing int
		var released bool
		if err := tx.QueryRow(ctx, `
			SELECT count(*), COALESCE(bool_or(status = 'released'), false)
			FROM inventory_reservations WHERE order_id = $1`, orderID).Scan(&existing, &released); err != nil {
			return apperr.Internal(err, "check reservation")
		}
		if released {
			return apperr.FailedPrecondition("reservation for this order was already released")
		}
		if existing > 0 {
			return nil // already reserved: idempotent retry
		}
		for _, ln := range lines {
			tag, err := tx.Exec(ctx, `
				UPDATE inventory SET available = available - $2, reserved = reserved + $2, updated_at = now()
				WHERE product_id = $1 AND available >= $2`, ln.ProductID, ln.Quantity)
			if err != nil {
				return apperr.Internal(err, "reserve stock")
			}
			if tag.RowsAffected() == 0 {
				return apperr.FailedPrecondition("insufficient stock for product %s", ln.ProductID)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO inventory_reservations (order_id, product_id, quantity) VALUES ($1, $2, $3)`,
				orderID, ln.ProductID, ln.Quantity); err != nil {
				if db.IsUniqueViolation(err) {
					// A concurrent call for the same order won the race.
					return apperr.Conflict("reservation in progress for this order")
				}
				return apperr.Internal(err, "insert reservation")
			}
		}
		return nil
	})
}

// Release returns reserved (not committed) stock of orderID. Idempotent.
func (r *Postgres) Release(ctx context.Context, orderID string) error {
	return r.settle(ctx, orderID, "released",
		`UPDATE inventory SET available = available + $2, reserved = reserved - $2, updated_at = now() WHERE product_id = $1`)
}

// Commit converts orderID's reservations into sales. Idempotent.
func (r *Postgres) Commit(ctx context.Context, orderID string) error {
	return r.settle(ctx, orderID, "committed",
		`UPDATE inventory SET reserved = reserved - $2, updated_at = now() WHERE product_id = $1`)
}

func (r *Postgres) settle(ctx context.Context, orderID, to, stockSQL string) error {
	return db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT product_id, quantity FROM inventory_reservations
			WHERE order_id = $1 AND status = 'reserved'
			ORDER BY product_id FOR UPDATE`, orderID)
		if err != nil {
			return apperr.Internal(err, "select reservations")
		}
		var lines []Line
		for rows.Next() {
			var l Line
			if err := rows.Scan(&l.ProductID, &l.Quantity); err != nil {
				rows.Close()
				return apperr.Internal(err, "scan reservation")
			}
			lines = append(lines, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return apperr.Internal(err, "iterate reservations")
		}
		for _, l := range lines {
			if _, err := tx.Exec(ctx, stockSQL, l.ProductID, l.Quantity); err != nil {
				return apperr.Internal(err, "settle stock")
			}
		}
		if len(lines) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE inventory_reservations SET status = $2, updated_at = now()
				WHERE order_id = $1 AND status = 'reserved'`, orderID, to); err != nil {
				return apperr.Internal(err, "update reservations")
			}
		}
		return nil
	})
}

// merge sums duplicate products and sorts by product id (lock ordering).
func merge(lines []Line) []Line {
	sum := map[string]int32{}
	for _, l := range lines {
		sum[l.ProductID] += l.Quantity
	}
	out := make([]Line, 0, len(sum))
	for id, q := range sum {
		out = append(out, Line{ProductID: id, Quantity: q})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProductID < out[j].ProductID })
	return out
}
