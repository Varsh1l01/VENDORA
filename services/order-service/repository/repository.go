// Package repository persists orders, their items and status history.
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/db"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/outbox"
	"github.com/Varsh1l01/VENDORA/pkg/pagination"
)

// Item is an order line with the price captured at checkout.
type Item struct {
	ProductID      string
	Name           string
	UnitPriceCents int64
	Quantity       int32
}

// Order is a stored order.
type Order struct {
	ID              string
	UserID          string
	Status          string
	TotalCents      int64
	Currency        string
	ShippingAddress string
	StatusReason    string
	IdempotencyKey  *string
	Items           []Item
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// OutEvent is an event to enqueue in the outbox with a state change.
type OutEvent struct {
	Topic    string
	Envelope events.Envelope
}

// Dedupe marks a state change as the effect of a consumed event.
type Dedupe struct {
	Consumer string
	EventID  string
}

// TransitionInput describes a conditional status change.
type TransitionInput struct {
	OrderID string
	From    []string
	To      string
	Reason  string
	Actor   string
	// Events builds the events to publish, given the updated order.
	Events func(o *Order) ([]OutEvent, error)
	Dedupe *Dedupe
}

// Postgres implements the order store.
type Postgres struct{ pool *pgxpool.Pool }

// New returns a Postgres store.
func New(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const orderCols = `id, user_id, status, total_cents, currency, shipping_address, status_reason, idempotency_key, created_at, updated_at`

func scanOrder(row pgx.Row) (*Order, error) {
	var o Order
	if err := row.Scan(&o.ID, &o.UserID, &o.Status, &o.TotalCents, &o.Currency, &o.ShippingAddress,
		&o.StatusReason, &o.IdempotencyKey, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

// Create inserts o (status PENDING) with its items. A duplicate idempotency
// key returns the existing order and created=false.
func (r *Postgres) Create(ctx context.Context, o *Order) (existing *Order, created bool, err error) {
	err = db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (id, user_id, status, total_cents, currency, shipping_address, idempotency_key, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
			o.ID, o.UserID, o.Status, o.TotalCents, o.Currency, o.ShippingAddress, o.IdempotencyKey, o.CreatedAt)
		if err != nil {
			return err
		}
		for _, it := range o.Items {
			if _, err := tx.Exec(ctx, `
				INSERT INTO order_items (order_id, product_id, product_name, unit_price_cents, quantity)
				VALUES ($1, $2, $3, $4, $5)`, o.ID, it.ProductID, it.Name, it.UnitPriceCents, it.Quantity); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO order_status_history (order_id, to_status, actor) VALUES ($1, $2, $3)`,
			o.ID, o.Status, o.UserID)
		return err
	})
	if err == nil {
		return nil, true, nil
	}
	if db.IsUniqueViolation(err) && o.IdempotencyKey != nil {
		ex, gerr := r.GetByIdempotencyKey(ctx, o.UserID, *o.IdempotencyKey)
		if gerr != nil {
			return nil, false, gerr
		}
		return ex, false, nil
	}
	if db.IsCheckViolation(err) {
		return nil, false, apperr.Invalid("order values are out of range")
	}
	return nil, false, apperr.Internal(err, "create order")
}

// GetByIdempotencyKey returns the user's order for key, or NotFound.
func (r *Postgres) GetByIdempotencyKey(ctx context.Context, userID, key string) (*Order, error) {
	o, err := scanOrder(r.pool.QueryRow(ctx, `SELECT `+orderCols+` FROM orders WHERE user_id = $1 AND idempotency_key = $2`, userID, key))
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("order not found")
	}
	if err != nil {
		return nil, apperr.Internal(err, "get order by key")
	}
	return o, r.loadItems(ctx, r.pool, []*Order{o})
}

// Get returns an order with its items.
func (r *Postgres) Get(ctx context.Context, id string) (*Order, error) {
	o, err := scanOrder(r.pool.QueryRow(ctx, `SELECT `+orderCols+` FROM orders WHERE id = $1`, id))
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("order not found")
	}
	if err != nil {
		return nil, apperr.Internal(err, "get order")
	}
	return o, r.loadItems(ctx, r.pool, []*Order{o})
}

// ListByUser returns up to limit orders of userID after cursor, newest first,
// with items loaded in a single extra query.
func (r *Postgres) ListByUser(ctx context.Context, userID string, limit int32, after *pagination.Cursor) ([]*Order, error) {
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = r.pool.Query(ctx, `SELECT `+orderCols+` FROM orders WHERE user_id = $1
			ORDER BY created_at DESC, id DESC LIMIT $2`, userID, limit)
	} else {
		rows, err = r.pool.Query(ctx, `SELECT `+orderCols+` FROM orders WHERE user_id = $1
			AND (created_at, id) < ($3, $4::uuid)
			ORDER BY created_at DESC, id DESC LIMIT $2`, userID, limit, after.CreatedAt, after.ID)
	}
	if err != nil {
		return nil, apperr.Internal(err, "list orders")
	}
	defer rows.Close()
	var out []*Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, apperr.Internal(err, "scan order")
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err, "iterate orders")
	}
	rows.Close()
	return out, r.loadItems(ctx, r.pool, out)
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (r *Postgres) loadItems(ctx context.Context, q querier, orders []*Order) error {
	if len(orders) == 0 {
		return nil
	}
	ids := make([]string, len(orders))
	byID := make(map[string]*Order, len(orders))
	for i, o := range orders {
		ids[i] = o.ID
		byID[o.ID] = o
		o.Items = nil
	}
	rows, err := q.Query(ctx, `
		SELECT order_id, product_id, product_name, unit_price_cents, quantity
		FROM order_items WHERE order_id = ANY($1::uuid[]) ORDER BY order_id, product_id`, ids)
	if err != nil {
		return apperr.Internal(err, "load items")
	}
	defer rows.Close()
	for rows.Next() {
		var oid string
		var it Item
		if err := rows.Scan(&oid, &it.ProductID, &it.Name, &it.UnitPriceCents, &it.Quantity); err != nil {
			return apperr.Internal(err, "scan item")
		}
		if o := byID[oid]; o != nil {
			o.Items = append(o.Items, it)
		}
	}
	if err := rows.Err(); err != nil {
		return apperr.Internal(err, "iterate items")
	}
	return nil
}

// ErrTransitionRejected is wrapped in the FailedPrecondition returned when the
// order is not in one of the expected source states.
var ErrTransitionRejected = fmt.Errorf("transition rejected")

// Transition atomically changes status when the current status is one of
// in.From, records history, marks the triggering event processed and
// enqueues events — all in one transaction.
//
// It returns (order, applied). applied=false with a nil error means the
// triggering event was a duplicate and nothing changed.
func (r *Postgres) Transition(ctx context.Context, in TransitionInput) (*Order, bool, error) {
	var out *Order
	applied := false
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		if in.Dedupe != nil {
			first, err := outbox.MarkProcessed(ctx, tx, in.Dedupe.Consumer, in.Dedupe.EventID)
			if err != nil {
				return apperr.Internal(err, "dedupe")
			}
			if !first {
				return nil
			}
		}
		var from string
		if err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1 FOR UPDATE`, in.OrderID).Scan(&from); err != nil {
			if db.IsNoRows(err) {
				return apperr.NotFound("order not found")
			}
			return apperr.Internal(err, "lock order")
		}
		allowed := false
		for _, f := range in.From {
			if f == from {
				allowed = true
				break
			}
		}
		if !allowed {
			return &apperr.Error{
				Kind:    apperr.KindFailedPrecondition,
				Message: fmt.Sprintf("order is %s and cannot become %s", from, in.To),
				Err:     ErrTransitionRejected,
			}
		}
		o, err := scanOrder(tx.QueryRow(ctx, `
			UPDATE orders SET status = $2, status_reason = $3, updated_at = now()
			WHERE id = $1 RETURNING `+orderCols, in.OrderID, in.To, in.Reason))
		if err != nil {
			return apperr.Internal(err, "update status")
		}
		if err := r.loadItems(ctx, tx, []*Order{o}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_status_history (order_id, from_status, to_status, reason, actor)
			VALUES ($1, $2, $3, $4, $5)`, in.OrderID, from, in.To, in.Reason, in.Actor); err != nil {
			return apperr.Internal(err, "insert history")
		}
		if in.Events != nil {
			evs, err := in.Events(o)
			if err != nil {
				return apperr.Internal(err, "build events")
			}
			for _, ev := range evs {
				if err := outbox.Enqueue(ctx, tx, ev.Topic, ev.Envelope); err != nil {
					return apperr.Internal(err, "enqueue")
				}
			}
		}
		out, applied = o, true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if !applied {
		o, err := r.Get(ctx, in.OrderID)
		return o, false, err
	}
	return out, true, nil
}

// Stale returns ids of orders in status that were last updated before
// cutoff (oldest first, at most limit).
func (r *Postgres) Stale(ctx context.Context, status string, cutoff time.Time, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id FROM orders WHERE status = $1 AND updated_at < $2
		ORDER BY updated_at LIMIT $3`, status, cutoff, limit)
	if err != nil {
		return nil, apperr.Internal(err, "select stale orders")
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, apperr.Internal(err, "scan stale order")
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
