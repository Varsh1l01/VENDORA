package service

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	cartv1 "github.com/Varsh1l01/VENDORA/gen/vendora/cart/v1"
	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/pagination"
	"github.com/Varsh1l01/VENDORA/services/order-service/repository"
)

// --- fakes -------------------------------------------------------------------

type fakeStore struct {
	mu        sync.Mutex
	orders    map[string]*repository.Order
	processed map[string]bool
	outbox    []events.Envelope
}

func newFakeStore() *fakeStore {
	return &fakeStore{orders: map[string]*repository.Order{}, processed: map[string]bool{}}
}

func clone(o *repository.Order) *repository.Order {
	c := *o
	c.Items = slices.Clone(o.Items)
	return &c
}

func (f *fakeStore) Create(_ context.Context, o *repository.Order) (*repository.Order, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o.IdempotencyKey != nil {
		for _, ex := range f.orders {
			if ex.UserID == o.UserID && ex.IdempotencyKey != nil && *ex.IdempotencyKey == *o.IdempotencyKey {
				return clone(ex), false, nil
			}
		}
	}
	f.orders[o.ID] = clone(o)
	return nil, true, nil
}

func (f *fakeStore) GetByIdempotencyKey(_ context.Context, userID, key string) (*repository.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.orders {
		if o.UserID == userID && o.IdempotencyKey != nil && *o.IdempotencyKey == key {
			return clone(o), nil
		}
	}
	return nil, apperr.NotFound("order not found")
}

func (f *fakeStore) Get(_ context.Context, id string) (*repository.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok {
		return nil, apperr.NotFound("order not found")
	}
	return clone(o), nil
}

func (f *fakeStore) ListByUser(context.Context, string, int32, *pagination.Cursor) ([]*repository.Order, error) {
	return nil, nil
}

func (f *fakeStore) Transition(_ context.Context, in repository.TransitionInput) (*repository.Order, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.Dedupe != nil {
		k := in.Dedupe.Consumer + "/" + in.Dedupe.EventID
		if f.processed[k] {
			return clone(f.orders[in.OrderID]), false, nil
		}
		defer func() { f.processed[k] = true }()
	}
	o, ok := f.orders[in.OrderID]
	if !ok {
		return nil, false, apperr.NotFound("order not found")
	}
	if !slices.Contains(in.From, o.Status) {
		return nil, false, apperr.FailedPrecondition("order is %s and cannot become %s", o.Status, in.To)
	}
	o.Status, o.StatusReason, o.UpdatedAt = in.To, in.Reason, time.Now()
	if in.Events != nil {
		evs, err := in.Events(clone(o))
		if err != nil {
			return nil, false, err
		}
		for _, e := range evs {
			f.outbox = append(f.outbox, e.Envelope)
		}
	}
	return clone(o), true, nil
}

func (f *fakeStore) Stale(_ context.Context, status string, cutoff time.Time, _ int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for id, o := range f.orders {
		if o.Status == status && o.UpdatedAt.Before(cutoff) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (f *fakeStore) eventTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.outbox {
		out = append(out, e.Type)
	}
	return out
}

type fakeCart struct{ items map[string][]*cartv1.CartItem }

func (c *fakeCart) GetCart(_ context.Context, uid string) (*cartv1.Cart, error) {
	return &cartv1.Cart{UserId: uid, Items: c.items[uid]}, nil
}

type fakeCatalog struct{ products map[string]*productv1.Product }

func (c *fakeCatalog) BatchGetProducts(_ context.Context, ids []string) ([]*productv1.Product, error) {
	var out []*productv1.Product
	for _, id := range ids {
		if p, ok := c.products[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

type fakeInventory struct {
	mu       sync.Mutex
	err      error
	reserved map[string][]Line
	released []string
}

func (i *fakeInventory) Reserve(_ context.Context, orderID string, lines []Line) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.err != nil {
		return i.err
	}
	if i.reserved == nil {
		i.reserved = map[string][]Line{}
	}
	i.reserved[orderID] = lines
	return nil
}

func (i *fakeInventory) Release(_ context.Context, orderID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.released = append(i.released, orderID)
	return nil
}

// --- fixtures ----------------------------------------------------------------

const (
	userA = "11111111-1111-4111-8111-111111111111"
	userB = "22222222-2222-4222-8222-222222222222"
	prod1 = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	prod2 = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

type env struct {
	svc   *Service
	store *fakeStore
	cart  *fakeCart
	cat   *fakeCatalog
	inv   *fakeInventory
}

func newEnv() *env {
	e := &env{
		store: newFakeStore(),
		cart: &fakeCart{items: map[string][]*cartv1.CartItem{
			// The cart's own price fields are deliberately wrong: the order
			// must use catalog prices.
			userA: {
				{ProductId: prod1, Quantity: 2, UnitPriceCents: 1},
				{ProductId: prod2, Quantity: 1, UnitPriceCents: 1},
			},
		}},
		cat: &fakeCatalog{products: map[string]*productv1.Product{
			prod1: {Id: prod1, Name: "Mouse", PriceCents: 2999, IsActive: true},
			prod2: {Id: prod2, Name: "Pad", PriceCents: 1000, IsActive: true},
		}},
		inv: &fakeInventory{},
	}
	e.svc = New(e.store, e.cart, e.cat, e.inv, Config{PaymentTimeout: time.Minute, PendingTimeout: time.Minute})
	return e
}

func as(uid, role string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{Subject: uid, Role: role})
}

// --- tests -------------------------------------------------------------------

func TestCreateUsesCatalogPricesAndAwaitsPayment(t *testing.T) {
	e := newEnv()
	o, err := e.svc.Create(as(userA, auth.RoleCustomer), CreateInput{IdempotencyKey: "k1", ShippingAddress: "1 Main St"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != string(AwaitingPayment) {
		t.Fatalf("status = %s", o.Status)
	}
	if want := int64(2*2999 + 1000); o.TotalCents != want {
		t.Fatalf("total = %d, want %d (catalog prices)", o.TotalCents, want)
	}
	if len(e.inv.reserved[o.ID]) != 2 {
		t.Fatalf("reservation lines = %v", e.inv.reserved[o.ID])
	}
	if got := e.store.eventTypes(); !slices.Equal(got, []string{events.OrderCreated}) {
		t.Fatalf("events = %v", got)
	}
}

func TestCreateIsIdempotent(t *testing.T) {
	e := newEnv()
	ctx := as(userA, auth.RoleCustomer)
	first, err := e.svc.Create(ctx, CreateInput{IdempotencyKey: "same"})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		again, err := e.svc.Create(ctx, CreateInput{IdempotencyKey: "same"})
		if err != nil || again.ID != first.ID {
			t.Fatalf("retry returned (%v, %v), want order %s", again, err, first.ID)
		}
	}
	if len(e.store.orders) != 1 || len(e.inv.reserved) != 1 {
		t.Fatalf("orders=%d reservations=%d, want 1/1", len(e.store.orders), len(e.inv.reserved))
	}
}

func TestCreateRejectsEmptyCartAndUnavailableProducts(t *testing.T) {
	e := newEnv()
	if _, err := e.svc.Create(as(userB, auth.RoleCustomer), CreateInput{}); !apperr.IsKind(err, apperr.KindFailedPrecondition) {
		t.Fatalf("empty cart: %v", err)
	}
	e.cat.products[prod2].IsActive = false
	if _, err := e.svc.Create(as(userA, auth.RoleCustomer), CreateInput{}); !apperr.IsKind(err, apperr.KindFailedPrecondition) {
		t.Fatalf("inactive product: %v", err)
	}
	if len(e.store.orders) != 0 {
		t.Fatal("no order should be created")
	}
}

func TestInsufficientStockRejectsOrder(t *testing.T) {
	e := newEnv()
	e.inv.err = apperr.FailedPrecondition("insufficient stock for product %s", prod1)
	_, err := e.svc.Create(as(userA, auth.RoleCustomer), CreateInput{IdempotencyKey: "k"})
	if !apperr.IsKind(err, apperr.KindFailedPrecondition) {
		t.Fatalf("err = %v", err)
	}
	for _, o := range e.store.orders {
		if o.Status != string(Rejected) {
			t.Fatalf("order status = %s, want REJECTED", o.Status)
		}
	}
	if len(e.store.eventTypes()) != 0 {
		t.Fatal("a rejected order must not emit order.created")
	}
}

func TestInventoryOutageCancelsAndCompensates(t *testing.T) {
	e := newEnv()
	e.inv.err = apperr.Unavailable(errors.New("timeout"), "reserve")
	_, err := e.svc.Create(as(userA, auth.RoleCustomer), CreateInput{})
	if !apperr.IsKind(err, apperr.KindUnavailable) {
		t.Fatalf("err = %v", err)
	}
	for _, o := range e.store.orders {
		if o.Status != string(Cancelled) {
			t.Fatalf("status = %s, want CANCELLED", o.Status)
		}
	}
	// order.cancelled drives inventory to release anything that may have
	// been reserved before the timeout.
	if got := e.store.eventTypes(); !slices.Equal(got, []string{events.OrderCancelled}) {
		t.Fatalf("events = %v", got)
	}
}

func TestGetHidesOtherUsersOrders(t *testing.T) {
	e := newEnv()
	o, err := e.svc.Create(as(userA, auth.RoleCustomer), CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(as(userB, auth.RoleCustomer), o.ID); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("other user got %v, want NotFound", err)
	}
	if _, err := e.svc.Get(as("admin", auth.RoleAdmin), o.ID); err != nil {
		t.Fatalf("admin: %v", err)
	}
	if _, err := e.svc.Cancel(as(userB, auth.RoleCustomer), o.ID); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("other user cancel: %v", err)
	}
}

func TestPaymentEventsAreAppliedOnce(t *testing.T) {
	e := newEnv()
	o, err := e.svc.Create(as(userA, auth.RoleCustomer), CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 3 { // duplicate deliveries
		if err := e.svc.HandlePaymentSucceeded(ctx, "order-service", "evt-1", o.ID); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := e.store.Get(ctx, o.ID)
	if got.Status != string(Confirmed) {
		t.Fatalf("status = %s", got.Status)
	}
	if n := countOf(e.store.eventTypes(), events.OrderConfirmed); n != 1 {
		t.Fatalf("order.confirmed emitted %d times", n)
	}
	// A late failure event for a paid order is ignored, not an error.
	if err := e.svc.HandlePaymentFailed(ctx, "order-service", "evt-2", o.ID, "declined"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.store.Get(ctx, o.ID); got.Status != string(Confirmed) {
		t.Fatalf("paid order changed to %s", got.Status)
	}
}

func TestCancelTwiceReleasesOnce(t *testing.T) {
	e := newEnv()
	ctx := as(userA, auth.RoleCustomer)
	o, err := e.svc.Create(ctx, CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Cancel(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Cancel(ctx, o.ID); !apperr.IsKind(err, apperr.KindFailedPrecondition) {
		t.Fatalf("second cancel: %v", err)
	}
	if n := countOf(e.store.eventTypes(), events.OrderCancelled); n != 1 {
		t.Fatalf("order.cancelled emitted %d times", n)
	}
}

func TestSweeperCancelsStaleOrders(t *testing.T) {
	e := newEnv()
	o, err := e.svc.Create(as(userA, auth.RoleCustomer), CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	e.svc.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	n, err := e.svc.ExpireStale(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("ExpireStale = (%d, %v)", n, err)
	}
	got, _ := e.store.Get(context.Background(), o.ID)
	if got.Status != string(Cancelled) || got.StatusReason != "payment timeout" {
		t.Fatalf("got %s (%s)", got.Status, got.StatusReason)
	}
}

func TestAdminFulfilmentTransitions(t *testing.T) {
	e := newEnv()
	o, _ := e.svc.Create(as(userA, auth.RoleCustomer), CreateInput{})
	adm := as("admin", auth.RoleAdmin)
	if _, err := e.svc.UpdateStatus(adm, o.ID, Shipped); !apperr.IsKind(err, apperr.KindFailedPrecondition) {
		t.Fatalf("shipping an unpaid order: %v", err)
	}
	_ = e.svc.HandlePaymentSucceeded(context.Background(), "c", "e", o.ID)
	for _, s := range []Status{Shipped, Delivered} {
		if _, err := e.svc.UpdateStatus(adm, o.ID, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if _, err := e.svc.UpdateStatus(adm, o.ID, Cancelled); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("manual cancel via status: %v", err)
	}
}

func countOf(xs []string, x string) int {
	n := 0
	for _, v := range xs {
		if v == x {
			n++
		}
	}
	return n
}
