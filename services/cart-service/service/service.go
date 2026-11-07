// Package service implements cart behaviour. Prices are never stored in the
// cart: every read prices lines from the catalog, so totals are authoritative.
package service

import (
	"context"
	"sort"

	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/validate"
	"github.com/Varsh1l01/VENDORA/services/cart-service/repository"
)

// Store is the cart persistence port.
type Store interface {
	Add(ctx context.Context, userID, productID string, qty, maxQty int32, maxLines int) error
	Set(ctx context.Context, userID, productID string, qty int32, maxLines int) error
	Remove(ctx context.Context, userID, productID string) error
	Clear(ctx context.Context, userID string) error
	Lines(ctx context.Context, userID string) ([]repository.Line, error)
}

// Catalog is the subset of ProductService used by the cart.
type Catalog interface {
	GetProduct(ctx context.Context, id string) (*productv1.Product, error)
	BatchGetProducts(ctx context.Context, ids []string) ([]*productv1.Product, error)
}

// Item is a priced cart line.
type Item struct {
	ProductID      string
	Name           string
	ImageURL       string
	UnitPriceCents int64
	Quantity       int32
	LineTotalCents int64
	Available      bool
}

// Cart is a priced cart.
type Cart struct {
	UserID     string
	Items      []Item
	TotalCents int64
	Currency   string
}

// Service is the cart service.
type Service struct {
	store   Store
	catalog Catalog
}

// New returns a Service.
func New(store Store, catalog Catalog) *Service { return &Service{store: store, catalog: catalog} }

// Get returns the caller's priced cart.
func (s *Service) Get(ctx context.Context, userID string) (*Cart, error) {
	uid, err := auth.ResolveUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.priced(ctx, uid)
}

// Add adds qty of an active product.
func (s *Service) Add(ctx context.Context, userID, productID string, qty int32) (*Cart, error) {
	uid, err := s.prepare(ctx, userID, productID, qty)
	if err != nil {
		return nil, err
	}
	if err := s.store.Add(ctx, uid, productID, qty, validate.MaxQuantityPerLine, validate.MaxCartLines); err != nil {
		return nil, err
	}
	return s.priced(ctx, uid)
}

// SetQuantity sets a line's quantity.
func (s *Service) SetQuantity(ctx context.Context, userID, productID string, qty int32) (*Cart, error) {
	uid, err := s.prepare(ctx, userID, productID, qty)
	if err != nil {
		return nil, err
	}
	if err := s.store.Set(ctx, uid, productID, qty, validate.MaxCartLines); err != nil {
		return nil, err
	}
	return s.priced(ctx, uid)
}

// Remove removes a line.
func (s *Service) Remove(ctx context.Context, userID, productID string) (*Cart, error) {
	uid, err := auth.ResolveUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := validate.ID("product_id", productID); err != nil {
		return nil, err
	}
	if err := s.store.Remove(ctx, uid, productID); err != nil {
		return nil, err
	}
	return s.priced(ctx, uid)
}

// Clear empties the cart.
func (s *Service) Clear(ctx context.Context, userID string) error {
	uid, err := auth.ResolveUser(ctx, userID)
	if err != nil {
		return err
	}
	return s.store.Clear(ctx, uid)
}

func (s *Service) prepare(ctx context.Context, userID, productID string, qty int32) (string, error) {
	uid, err := auth.ResolveUser(ctx, userID)
	if err != nil {
		return "", err
	}
	if err := validate.ID("product_id", productID); err != nil {
		return "", err
	}
	if err := validate.Quantity(qty); err != nil {
		return "", err
	}
	p, err := s.catalog.GetProduct(ctx, productID)
	if err != nil {
		return "", err
	}
	if !p.GetIsActive() {
		return "", apperr.NotFound("product not found")
	}
	return uid, nil
}

func (s *Service) priced(ctx context.Context, uid string) (*Cart, error) {
	lines, err := s.store.Lines(ctx, uid)
	if err != nil {
		return nil, err
	}
	cart := &Cart{UserID: uid, Currency: "USD", Items: make([]Item, 0, len(lines))}
	if len(lines) == 0 {
		return cart, nil
	}
	ids := make([]string, len(lines))
	for i, l := range lines {
		ids[i] = l.ProductID
	}
	products, err := s.catalog.BatchGetProducts(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*productv1.Product, len(products))
	for _, p := range products {
		byID[p.GetId()] = p
	}
	for _, l := range lines {
		it := Item{ProductID: l.ProductID, Quantity: l.Quantity}
		if p, ok := byID[l.ProductID]; ok {
			it.Name, it.ImageURL, it.UnitPriceCents = p.GetName(), p.GetImageUrl(), p.GetPriceCents()
			it.Available = p.GetIsActive()
		}
		if it.Available {
			it.LineTotalCents = it.UnitPriceCents * int64(it.Quantity)
			cart.TotalCents += it.LineTotalCents
		}
		cart.Items = append(cart.Items, it)
	}
	sort.Slice(cart.Items, func(i, j int) bool { return cart.Items[i].ProductID < cart.Items[j].ProductID })
	return cart, nil
}
