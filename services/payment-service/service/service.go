// Package service implements payments.
//
// The amount charged always comes from the order (never from the client),
// each order has at most one payment, and provider calls carry a stable
// idempotency key (the payment id), so repeated PayOrder calls are safe.
package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	orderv1 "github.com/Varsh1l01/VENDORA/gen/vendora/order/v1"
	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/pkg/requestid"
	"github.com/Varsh1l01/VENDORA/pkg/validate"
	"github.com/Varsh1l01/VENDORA/services/payment-service/provider"
	"github.com/Varsh1l01/VENDORA/services/payment-service/repository"
)

// Payment statuses.
const (
	Pending   = "PENDING"
	Succeeded = "SUCCEEDED"
	Failed    = "FAILED"
	Refunded  = "REFUNDED"
)

// Store is the persistence port.
type Store interface {
	GetOrCreatePending(ctx context.Context, p *repository.Payment) (*repository.Payment, error)
	GetByOrder(ctx context.Context, orderID string) (*repository.Payment, error)
	Settle(ctx context.Context, id string, from []string, status, reason, ref string,
		ev func(*repository.Payment) (string, events.Envelope, error)) (*repository.Payment, bool, error)
	MarkEventProcessed(ctx context.Context, consumer, eventID string) (bool, error)
}

// Orders reads orders from order-service.
type Orders interface {
	GetOrder(ctx context.Context, orderID, userID string) (*orderv1.Order, error)
}

// Service is the payment service.
type Service struct {
	store    Store
	orders   Orders
	provider provider.Provider
}

// New returns a Service.
func New(store Store, orders Orders, p provider.Provider) *Service {
	return &Service{store: store, orders: orders, provider: p}
}

// Pay charges the order's total. Idempotent per order.
func (s *Service) Pay(ctx context.Context, orderID, userID, token string) (*repository.Payment, error) {
	uid, err := auth.ResolveUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := validate.ID("order_id", orderID); err != nil {
		return nil, err
	}
	if len(token) > 200 {
		return nil, apperr.Invalid("payment_method_token is too long")
	}

	if p, err := s.store.GetByOrder(ctx, orderID); err == nil {
		if p.UserID != uid {
			return nil, apperr.NotFound("order not found")
		}
		switch p.Status {
		case Succeeded, Failed, Refunded:
			return p, nil // already settled: idempotent result
		}
	} else if !apperr.IsKind(err, apperr.KindNotFound) {
		return nil, err
	}

	order, err := s.orders.GetOrder(ctx, orderID, uid)
	if err != nil {
		return nil, err
	}
	if order.GetStatus() != orderv1.OrderStatus_ORDER_STATUS_AWAITING_PAYMENT {
		return nil, apperr.FailedPrecondition("order is not awaiting payment")
	}

	p, err := s.store.GetOrCreatePending(ctx, &repository.Payment{
		ID: uuid.NewString(), OrderID: orderID, UserID: uid,
		AmountCents: order.GetTotalCents(), Currency: order.GetCurrency(),
	})
	if err != nil {
		return nil, err
	}
	if p.Status != Pending {
		return p, nil
	}

	res, err := s.provider.Charge(ctx, provider.ChargeRequest{
		IdempotencyKey: p.ID, AmountCents: p.AmountCents, Currency: p.Currency, Token: token,
	})
	if err != nil {
		if errors.Is(err, provider.ErrTransient) || ctx.Err() != nil {
			// Payment stays PENDING; the client may retry PayOrder, which reuses
			// the same idempotency key at the provider.
			return nil, apperr.Unavailable(err, "charge")
		}
		return nil, apperr.Internal(err, "charge")
	}
	log := logx.From(ctx).With("order_id", orderID, "payment_id", p.ID)
	if res.Declined {
		out, _, err := s.store.Settle(ctx, p.ID, []string{Pending}, Failed, res.Reason, "", s.event(ctx, events.PaymentFailed))
		log.Info("payment declined", "reason", res.Reason)
		return out, err
	}
	out, _, err := s.store.Settle(ctx, p.ID, []string{Pending}, Succeeded, "", res.Reference, s.event(ctx, events.PaymentSucceeded))
	if err == nil {
		log.Info("payment succeeded", "amount_cents", out.AmountCents)
	}
	return out, err
}

// GetByOrder returns an order's payment visible to the caller.
func (s *Service) GetByOrder(ctx context.Context, orderID string) (*repository.Payment, error) {
	if err := validate.ID("order_id", orderID); err != nil {
		return nil, err
	}
	p, err := s.store.GetByOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !auth.CanAccessOwnedBy(ctx, p.UserID) {
		return nil, apperr.NotFound("payment not found")
	}
	return p, nil
}

// HandleOrderCancelled refunds a successful charge whose order was
// cancelled afterwards (e.g. it timed out while the charge was in flight).
func (s *Service) HandleOrderCancelled(ctx context.Context, consumer, eventID, orderID string) error {
	p, err := s.store.GetByOrder(ctx, orderID)
	if apperr.IsKind(err, apperr.KindNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if p.Status != Succeeded {
		return nil
	}
	first, err := s.store.MarkEventProcessed(ctx, consumer, eventID)
	if err != nil || !first {
		return err
	}
	if err := s.provider.Refund(ctx, p.ProviderReference, p.AmountCents); err != nil {
		return err // retried by the consumer; refund at a real PSP must be idempotent too
	}
	_, _, err = s.store.Settle(ctx, p.ID, []string{Succeeded}, Refunded, "order cancelled", "", s.event(ctx, events.PaymentRefunded))
	if err == nil {
		logx.From(ctx).Warn("payment refunded for cancelled order", "order_id", orderID, "payment_id", p.ID)
	}
	return err
}

func (s *Service) event(ctx context.Context, typ string) func(*repository.Payment) (string, events.Envelope, error) {
	return func(p *repository.Payment) (string, events.Envelope, error) {
		var payload any
		switch typ {
		case events.PaymentSucceeded:
			payload = events.PaymentSucceededData{PaymentID: p.ID, OrderID: p.OrderID, UserID: p.UserID, AmountCents: p.AmountCents, Currency: p.Currency}
		case events.PaymentFailed:
			payload = events.PaymentFailedData{PaymentID: p.ID, OrderID: p.OrderID, UserID: p.UserID, Reason: p.FailureReason}
		case events.PaymentRefunded:
			payload = events.PaymentRefundedData{PaymentID: p.ID, OrderID: p.OrderID, UserID: p.UserID, AmountCents: p.AmountCents, Currency: p.Currency}
		}
		ev, err := events.New(typ, p.OrderID, requestid.From(ctx), payload)
		return events.TopicPayment, ev, err
	}
}
