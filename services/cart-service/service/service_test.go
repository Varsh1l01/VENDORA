package service

import (
	"context"
	"testing"

	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/services/cart-service/repository"
)

const (
	u1 = "11111111-1111-4111-8111-111111111111"
	p1 = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	p2 = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

type memStore struct{ carts map[string]map[string]int32 }

func (m *memStore) cart(u string) map[string]int32 {
	if m.carts[u] == nil {
		m.carts[u] = map[string]int32{}
	}
	return m.carts[u]
}

func (m *memStore) Add(_ context.Context, u, p string, q, max int32, _ int) error {
	c := m.cart(u)
	c[p] = min(c[p]+q, max)
	return nil
}
func (m *memStore) Set(_ context.Context, u, p string, q int32, _ int) error {
	m.cart(u)[p] = q
	return nil
}
func (m *memStore) Remove(_ context.Context, u, p string) error { delete(m.cart(u), p); return nil }
func (m *memStore) Clear(_ context.Context, u string) error     { delete(m.carts, u); return nil }
func (m *memStore) Lines(_ context.Context, u string) ([]repository.Line, error) {
	var out []repository.Line
	for p, q := range m.carts[u] {
		out = append(out, repository.Line{ProductID: p, Quantity: q})
	}
	return out, nil
}

type catalog map[string]*productv1.Product

func (c catalog) GetProduct(_ context.Context, id string) (*productv1.Product, error) {
	if p, ok := c[id]; ok {
		return p, nil
	}
	return nil, apperr.NotFound("product not found")
}

func (c catalog) BatchGetProducts(_ context.Context, ids []string) ([]*productv1.Product, error) {
	var out []*productv1.Product
	for _, id := range ids {
		if p, ok := c[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func setup() (*Service, catalog, context.Context) {
	cat := catalog{
		p1: {Id: p1, Name: "Mouse", PriceCents: 2999, IsActive: true},
		p2: {Id: p2, Name: "Pad", PriceCents: 1000, IsActive: true},
	}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Subject: u1, Role: auth.RoleCustomer})
	return New(&memStore{carts: map[string]map[string]int32{}}, cat), cat, ctx
}

func TestCartPricesFromCatalog(t *testing.T) {
	svc, cat, ctx := setup()
	if _, err := svc.Add(ctx, "", p1, 2); err != nil {
		t.Fatal(err)
	}
	c, err := svc.Add(ctx, "", p2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.TotalCents != 2*2999+1000 {
		t.Fatalf("total = %d", c.TotalCents)
	}
	// A price change is reflected immediately: prices are never stored.
	cat[p1].PriceCents = 2500
	c, _ = svc.Get(ctx, "")
	if c.TotalCents != 2*2500+1000 {
		t.Fatalf("total after price change = %d", c.TotalCents)
	}
	// A deactivated product stays visible but no longer counts.
	cat[p2].IsActive = false
	c, _ = svc.Get(ctx, "")
	if c.TotalCents != 5000 {
		t.Fatalf("total with inactive item = %d", c.TotalCents)
	}
}

func TestCartValidation(t *testing.T) {
	svc, cat, ctx := setup()
	for _, q := range []int32{-3, 0, 100} {
		if _, err := svc.Add(ctx, "", p1, q); !apperr.IsKind(err, apperr.KindInvalid) {
			t.Errorf("qty %d: %v", q, err)
		}
	}
	if _, err := svc.Add(ctx, "", "not-a-uuid", 1); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Errorf("bad id: %v", err)
	}
	if _, err := svc.Add(ctx, "", "cccccccc-cccc-4ccc-8ccc-cccccccccccc", 1); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Errorf("unknown product: %v", err)
	}
	cat[p1].IsActive = false
	if _, err := svc.Add(ctx, "", p1, 1); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Errorf("inactive product: %v", err)
	}
}

func TestCartOwnership(t *testing.T) {
	svc, _, ctx := setup()
	if _, err := svc.Get(ctx, "22222222-2222-4222-8222-222222222222"); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("read other cart: %v", err)
	}
	if _, err := svc.Get(context.Background(), ""); !apperr.IsKind(err, apperr.KindUnauthenticated) {
		t.Fatalf("anonymous: %v", err)
	}
}
