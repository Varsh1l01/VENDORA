// Package service implements the order lifecycle and the checkout saga.
//
// Checkout (orchestrated saga):
//
//  1. Create the order as PENDING with authoritative catalog prices
//     (idempotent per user + Idempotency-Key).
//  2. Reserve all lines in inventory-service in one atomic, idempotent call.
//     Insufficient stock -> REJECTED. Unknown outcome -> CANCELLED, whose
//     order.cancelled event makes inventory release anything reserved.
//  3. Move to AWAITING_PAYMENT and emit order.created (outbox).
//  4. payment.succeeded -> CONFIRMED (+ order.confirmed: inventory commits).
//     payment.failed / user cancel / timeout -> CANCELLED (+ order.cancelled:
//     inventory releases, payment-service refunds a late successful charge).
//
// All state changes are conditional transitions written together with
// their outbox events, so no event is lost and no stale transition wins.
package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	cartv1 "github.com/Varsh1l01/VENDORA/gen/vendora/cart/v1"
	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/pkg/pagination"
	"github.com/Varsh1l01/VENDORA/pkg/requestid"
	"github.com/Varsh1l01/VENDORA/pkg/validate"
	"github.com/Varsh1l01/VENDORA/services/order-service/repository"
)

// Store is the persistence port.
type Store interface {
	Create(ctx context.Context, o *repository.Order) (*repository.Order, bool, error)
	GetByIdempotencyKey(ctx context.Context, userID, key string) (*repository.Order, error)
	Get(ctx context.Context, id string) (*repository.Order, error)
	ListByUser(ctx context.Context, userID string, limit int32, after *pagination.Cursor) ([]*repository.Order, error)
	Transition(ctx context.Context, in repository.TransitionInput) (*repository.Order, bool, error)
	Stale(ctx context.Context, status string, cutoff time.Time, limit int) ([]string, error)
}

// Cart reads a user's cart (quantities).
type Cart interface {
	GetCart(ctx context.Context, userID string) (*cartv1.Cart, error)
}

// Catalog reads authoritative product data.
type Catalog interface {
	BatchGetProducts(ctx context.Context, ids []string) ([]*productv1.Product, error)
}

// Line is a product quantity.
type Line struct {
	ProductID string
	Quantity  int32
}

// Inventory reserves and releases stock.
type Inventory interface {
	Reserve(ctx context.Context, orderID string, lines []Line) error
	Release(ctx context.Context, orderID string) error
}

// Config tunes saga timeouts.
type Config struct {
	PaymentTimeout time.Duration // AWAITING_PAYMENT older than this is cancelled
	PendingTimeout time.Duration // PENDING older than this is cancelled
}

// Service is the order service.
type Service struct {
	store     Store
	cart      Cart
	catalog   Catalog
	inventory Inventory
	cfg       Config
	now       func() time.Time
}

// New returns a Service.
func New(store Store, cart Cart, catalog Catalog, inv Inventory, cfg Config) *Service {
	if cfg.PaymentTimeout == 0 {
		cfg.PaymentTimeout = 15 * time.Minute
	}
	if cfg.PendingTimeout == 0 {
		cfg.PendingTimeout = 2 * time.Minute
	}
	return &Service{store: store, cart: cart, catalog: catalog, inventory: inv, cfg: cfg, now: time.Now}
}

// CreateInput is a checkout request.
type CreateInput struct {
	UserID          string
	IdempotencyKey  string
	ShippingAddress string
}

// Create checks out the user's cart. See the package documentation.
func (s *Service) Create(ctx context.Context, in CreateInput) (*repository.Order, error) {
	uid, err := auth.ResolveUser(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	var key *string
	if in.IdempotencyKey != "" {
		k, err := validate.Text("idempotency_key", in.IdempotencyKey, 1, 100)
		if err != nil {
			return nil, err
		}
		key = &k
		if existing, err := s.store.GetByIdempotencyKey(ctx, uid, k); err == nil {
			return existing, nil
		} else if !apperr.IsKind(err, apperr.KindNotFound) {
			return nil, err
		}
	}
	addr, err := validate.Text("shipping_address", in.ShippingAddress, 0, 500)
	if err != nil {
		return nil, err
	}

	items, err := s.priceCart(ctx, uid)
	if err != nil {
		return nil, err
	}
	var total int64
	for _, it := range items {
		total += it.UnitPriceCents * int64(it.Quantity)
	}
	now := s.now().UTC()
	o := &repository.Order{
		ID: uuid.NewString(), UserID: uid, Status: string(Pending), TotalCents: total, Currency: "USD",
		ShippingAddress: addr, IdempotencyKey: key, Items: items, CreatedAt: now, UpdatedAt: now,
	}
	existing, created, err := s.store.Create(ctx, o)
	if err != nil {
		return nil, err
	}
	if !created {
		return existing, nil // concurrent duplicate with the same key
	}
	log := logx.From(ctx).With("order_id", o.ID)

	lines := make([]Line, len(items))
	for i, it := range items {
		lines[i] = Line{ProductID: it.ProductID, Quantity: it.Quantity}
	}
	if err := s.inventory.Reserve(ctx, o.ID, lines); err != nil {
		if apperr.IsKind(err, apperr.KindFailedPrecondition) {
			var ae *apperr.Error
			reason := "insufficient stock"
			if errors.As(err, &ae) {
				reason = ae.Message
			}
			if _, _, terr := s.transition(ctx, o.ID, Rejected, reason, "system", nil); terr != nil {
				log.Error("failed to reject order", "error", terr)
			}
			return nil, apperr.FailedPrecondition("%s", reason)
		}
		// Outcome unknown (timeout, unavailable): cancel. order.cancelled makes
		// inventory release whatever may have been reserved.
		log.Error("reservation failed, cancelling order", "error", err)
		if _, _, terr := s.transition(ctx, o.ID, Cancelled, "inventory unavailable", "system", s.cancelledEvent(ctx)); terr != nil {
			log.Error("failed to cancel order; sweeper will retry", "error", terr)
		}
		return nil, apperr.Unavailable(err, "reserve inventory")
	}

	updated, _, err := s.transition(ctx, o.ID, AwaitingPayment, "", "system", s.createdEvent(ctx))
	if err != nil {
		// Stock is reserved but the order is still PENDING; the sweeper
		// cancels it after PendingTimeout, which releases the stock.
		return nil, err
	}
	log.Info("order created", "total_cents", total, "lines", len(items))
	return updated, nil
}

// priceCart reads quantities from the cart and prices from the catalog.
func (s *Service) priceCart(ctx context.Context, uid string) ([]repository.Item, error) {
	cart, err := s.cart.GetCart(ctx, uid)
	if err != nil {
		return nil, err
	}
	if len(cart.GetItems()) == 0 {
		return nil, apperr.FailedPrecondition("cart is empty")
	}
	if len(cart.GetItems()) > validate.MaxCartLines {
		return nil, apperr.FailedPrecondition("cart has too many lines")
	}
	ids := make([]string, 0, len(cart.GetItems()))
	for _, it := range cart.GetItems() {
		ids = append(ids, it.GetProductId())
	}
	products, err := s.catalog.BatchGetProducts(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*productv1.Product, len(products))
	for _, p := range products {
		byID[p.GetId()] = p
	}
	items := make([]repository.Item, 0, len(ids))
	for _, ci := range cart.GetItems() {
		p, ok := byID[ci.GetProductId()]
		if !ok || !p.GetIsActive() {
			return nil, apperr.FailedPrecondition("product %s is no longer available; remove it from the cart", ci.GetProductId())
		}
		if err := validate.Quantity(ci.GetQuantity()); err != nil {
			return nil, err
		}
		items = append(items, repository.Item{
			ProductID: p.GetId(), Name: p.GetName(), UnitPriceCents: p.GetPriceCents(), Quantity: ci.GetQuantity(),
		})
	}
	return items, nil
}

// Get returns an order visible to the caller. Other users' orders are
// reported as not found so their existence is not revealed.
func (s *Service) Get(ctx context.Context, id string) (*repository.Order, error) {
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	o, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !auth.CanAccessOwnedBy(ctx, o.UserID) {
		return nil, apperr.NotFound("order not found")
	}
	return o, nil
}

// List returns a page of the caller's orders.
func (s *Service) List(ctx context.Context, userID string, pageSize int32, token string) ([]*repository.Order, string, error) {
	uid, err := auth.ResolveUser(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	cur, err := pagination.Decode(token)
	if err != nil {
		return nil, "", err
	}
	n := validate.PageSize(pageSize)
	os, err := s.store.ListByUser(ctx, uid, n+1, cur)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if int32(len(os)) > n {
		os = os[:n]
		last := os[len(os)-1]
		next = pagination.Encode(pagination.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return os, next, nil
}

// Cancel cancels an unpaid order of the caller.
func (s *Service) Cancel(ctx context.Context, id string) (*repository.Order, error) {
	o, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	p, _ := auth.FromContext(ctx)
	updated, _, err := s.transition(ctx, o.ID, Cancelled, "cancelled by customer", p.Subject, s.cancelledEvent(ctx))
	return updated, err
}

// UpdateStatus performs admin fulfilment transitions.
func (s *Service) UpdateStatus(ctx context.Context, id string, to Status) (*repository.Order, error) {
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	if to != Shipped && to != Delivered {
		return nil, apperr.Invalid("only SHIPPED and DELIVERED can be set manually")
	}
	p, _ := auth.FromContext(ctx)
	updated, _, err := s.transition(ctx, id, to, "", p.Subject, func(o *repository.Order) ([]repository.OutEvent, error) {
		return one(ctx, events.TopicOrder, events.OrderStatusChanged, o.ID,
			events.OrderStatusChangedData{OrderID: o.ID, UserID: o.UserID, Status: o.Status})
	})
	return updated, err
}

// HandlePaymentSucceeded confirms the order (exactly once per event).
func (s *Service) HandlePaymentSucceeded(ctx context.Context, consumer, eventID, orderID string) error {
	_, _, err := s.transitionDedupe(ctx, orderID, Confirmed, "", "payment-service", s.confirmedEvent(ctx), consumer, eventID)
	return ignoreRejected(ctx, err, orderID, Confirmed)
}

// HandlePaymentFailed cancels the order (exactly once per event).
func (s *Service) HandlePaymentFailed(ctx context.Context, consumer, eventID, orderID, reason string) error {
	if reason == "" {
		reason = "payment failed"
	}
	_, _, err := s.transitionDedupe(ctx, orderID, Cancelled, reason, "payment-service", s.cancelledEvent(ctx), consumer, eventID)
	return ignoreRejected(ctx, err, orderID, Cancelled)
}

// ExpireStale cancels orders stuck in PENDING or AWAITING_PAYMENT. Safe to
// run on every replica concurrently: transitions are conditional.
func (s *Service) ExpireStale(ctx context.Context) (int, error) {
	n := 0
	for _, c := range []struct {
		status Status
		ttl    time.Duration
		reason string
	}{
		{Pending, s.cfg.PendingTimeout, "checkout did not complete"},
		{AwaitingPayment, s.cfg.PaymentTimeout, "payment timeout"},
	} {
		ids, err := s.store.Stale(ctx, string(c.status), s.now().Add(-c.ttl), 100)
		if err != nil {
			return n, err
		}
		for _, id := range ids {
			_, _, err := s.store.Transition(ctx, repository.TransitionInput{
				OrderID: id, From: []string{string(c.status)}, To: string(Cancelled),
				Reason: c.reason, Actor: "sweeper", Events: s.cancelledEvent(ctx),
			})
			if err != nil {
				if apperr.IsKind(err, apperr.KindFailedPrecondition) {
					continue // changed concurrently
				}
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// RunSweeper calls ExpireStale every interval until ctx is cancelled.
func (s *Service) RunSweeper(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			n, err := s.ExpireStale(ctx)
			if err != nil && ctx.Err() == nil {
				logx.From(ctx).Error("order sweeper failed", "error", err)
			} else if n > 0 {
				logx.From(ctx).Info("expired stale orders", "count", n)
			}
		}
	}
}

func (s *Service) transition(ctx context.Context, id string, to Status, reason, actor string,
	evs eventsFn) (*repository.Order, bool, error) {
	return s.store.Transition(ctx, repository.TransitionInput{
		OrderID: id, From: statuses(SourcesOf(to)), To: string(to), Reason: reason, Actor: actor, Events: evs,
	})
}

func (s *Service) transitionDedupe(ctx context.Context, id string, to Status, reason, actor string,
	evs eventsFn, consumer, eventID string) (*repository.Order, bool, error) {
	return s.store.Transition(ctx, repository.TransitionInput{
		OrderID: id, From: statuses(SourcesOf(to)), To: string(to), Reason: reason, Actor: actor, Events: evs,
		Dedupe: &repository.Dedupe{Consumer: consumer, EventID: eventID},
	})
}

// ignoreRejected treats an illegal transition caused by an event as handled:
// e.g. payment.succeeded for an order already cancelled by the sweeper. The
// compensating refund is driven by the order.cancelled event.
func ignoreRejected(ctx context.Context, err error, orderID string, to Status) error {
	if err != nil && apperr.IsKind(err, apperr.KindFailedPrecondition) {
		logx.From(ctx).Warn("event-driven transition not applicable", "order_id", orderID, "to", to, "reason", err.Error())
		return nil
	}
	return err
}

type eventsFn = func(*repository.Order) ([]repository.OutEvent, error)

func (s *Service) createdEvent(ctx context.Context) eventsFn {
	return func(o *repository.Order) ([]repository.OutEvent, error) {
		lines := make([]events.OrderLine, len(o.Items))
		for i, it := range o.Items {
			lines[i] = events.OrderLine{ProductID: it.ProductID, Quantity: it.Quantity}
		}
		return one(ctx, events.TopicOrder, events.OrderCreated, o.ID, events.OrderCreatedData{
			OrderID: o.ID, UserID: o.UserID, TotalCents: o.TotalCents, Currency: o.Currency, Lines: lines,
		})
	}
}

func (s *Service) confirmedEvent(ctx context.Context) eventsFn {
	return func(o *repository.Order) ([]repository.OutEvent, error) {
		return one(ctx, events.TopicOrder, events.OrderConfirmed, o.ID,
			events.OrderConfirmedData{OrderID: o.ID, UserID: o.UserID})
	}
}

func (s *Service) cancelledEvent(ctx context.Context) eventsFn {
	return func(o *repository.Order) ([]repository.OutEvent, error) {
		return one(ctx, events.TopicOrder, events.OrderCancelled, o.ID,
			events.OrderCancelledData{OrderID: o.ID, UserID: o.UserID, Reason: o.StatusReason})
	}
}

func one(ctx context.Context, topic, typ, aggregate string, payload any) ([]repository.OutEvent, error) {
	ev, err := events.New(typ, aggregate, requestid.From(ctx), payload)
	if err != nil {
		return nil, err
	}
	return []repository.OutEvent{{Topic: topic, Envelope: ev}}, nil
}

func statuses(ss []Status) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = string(s)
	}
	return out
}
