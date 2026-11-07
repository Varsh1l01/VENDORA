// Package repository persists payments.
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
)

// Payment is a stored payment.
type Payment struct {
	ID                string
	OrderID           string
	UserID            string
	AmountCents       int64
	Currency          string
	Status            string
	FailureReason     string
	ProviderReference string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Postgres implements the payment store.
type Postgres struct{ pool *pgxpool.Pool }

// New returns a Postgres store.
func New(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const cols = `id, order_id, user_id, amount_cents, currency, status, failure_reason, provider_reference, created_at, updated_at`

func scan(row pgx.Row) (*Payment, error) {
	var p Payment
	if err := row.Scan(&p.ID, &p.OrderID, &p.UserID, &p.AmountCents, &p.Currency, &p.Status,
		&p.FailureReason, &p.ProviderReference, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetOrCreatePending inserts a PENDING payment for the order or returns the
// existing one (the UNIQUE(order_id) constraint makes this race-free).
func (r *Postgres) GetOrCreatePending(ctx context.Context, p *Payment) (*Payment, error) {
	out, err := scan(r.pool.QueryRow(ctx, `
		INSERT INTO payments (id, order_id, user_id, amount_cents, currency, status)
		VALUES ($1, $2, $3, $4, $5, 'PENDING')
		ON CONFLICT (order_id) DO UPDATE SET order_id = EXCLUDED.order_id
		RETURNING `+cols, p.ID, p.OrderID, p.UserID, p.AmountCents, p.Currency))
	if err != nil {
		return nil, apperr.Internal(err, "upsert payment")
	}
	return out, nil
}

// GetByOrder returns the payment of an order.
func (r *Postgres) GetByOrder(ctx context.Context, orderID string) (*Payment, error) {
	p, err := scan(r.pool.QueryRow(ctx, `SELECT `+cols+` FROM payments WHERE order_id = $1`, orderID))
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("payment not found")
	}
	if err != nil {
		return nil, apperr.Internal(err, "get payment")
	}
	return p, nil
}

// Settle moves a payment from one of from to status and enqueues ev, in one
// transaction. It returns the current payment and whether it changed.
func (r *Postgres) Settle(ctx context.Context, id string, from []string, status, reason, ref string,
	ev func(*Payment) (string, events.Envelope, error)) (*Payment, bool, error) {
	var out *Payment
	changed := false
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		p, err := scan(tx.QueryRow(ctx, `
			UPDATE payments SET status = $3, failure_reason = $4,
				provider_reference = CASE WHEN $5 = '' THEN provider_reference ELSE $5 END, updated_at = now()
			WHERE id = $1 AND status = ANY($2)
			RETURNING `+cols, id, from, status, reason, ref))
		if db.IsNoRows(err) {
			return nil
		}
		if err != nil {
			return apperr.Internal(err, "settle payment")
		}
		if ev != nil {
			topic, env, err := ev(p)
			if err != nil {
				return apperr.Internal(err, "build event")
			}
			if err := outbox.Enqueue(ctx, tx, topic, env); err != nil {
				return apperr.Internal(err, "enqueue payment event")
			}
		}
		out, changed = p, true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if !changed {
		var gerr error
		out, gerr = scan(r.pool.QueryRow(ctx, `SELECT `+cols+` FROM payments WHERE id = $1`, id))
		if gerr != nil {
			return nil, false, apperr.Internal(fmt.Errorf("reload payment: %w", gerr), "settle")
		}
	}
	return out, changed, nil
}

// MarkEventProcessed records a consumed event; false means duplicate.
func (r *Postgres) MarkEventProcessed(ctx context.Context, consumer, eventID string) (bool, error) {
	var first bool
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		first, err = outbox.MarkProcessed(ctx, tx, consumer, eventID)
		return err
	})
	return first, err
}
