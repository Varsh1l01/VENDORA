// Package service validates inventory operations.
package service

import (
	"context"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/validate"
	"github.com/Varsh1l01/VENDORA/services/inventory-service/repository"
)

// Store is the persistence port.
type Store interface {
	Get(ctx context.Context, productID string) (*repository.Stock, error)
	Set(ctx context.Context, productID string, available int32) (*repository.Stock, error)
	Adjust(ctx context.Context, productID string, delta int32) (*repository.Stock, error)
	Reserve(ctx context.Context, orderID string, lines []repository.Line) error
	Release(ctx context.Context, orderID string) error
	Commit(ctx context.Context, orderID string) error
}

// Service is the inventory service.
type Service struct{ store Store }

// New returns a Service.
func New(store Store) *Service { return &Service{store: store} }

// Get returns stock for a product.
func (s *Service) Get(ctx context.Context, productID string) (*repository.Stock, error) {
	if err := validate.ID("product_id", productID); err != nil {
		return nil, err
	}
	return s.store.Get(ctx, productID)
}

// Set sets absolute available stock.
func (s *Service) Set(ctx context.Context, productID string, available int32) (*repository.Stock, error) {
	if err := validate.ID("product_id", productID); err != nil {
		return nil, err
	}
	if err := validate.Stock(available); err != nil {
		return nil, err
	}
	return s.store.Set(ctx, productID, available)
}

// Adjust changes available stock by delta.
func (s *Service) Adjust(ctx context.Context, productID string, delta int32) (*repository.Stock, error) {
	if err := validate.ID("product_id", productID); err != nil {
		return nil, err
	}
	if delta == 0 || delta > validate.MaxStock || delta < -validate.MaxStock {
		return nil, apperr.Invalid("delta must be non-zero and within ±%d", validate.MaxStock)
	}
	return s.store.Adjust(ctx, productID, delta)
}

// Reserve validates and reserves all lines for an order.
func (s *Service) Reserve(ctx context.Context, orderID string, lines []repository.Line) error {
	if err := validate.ID("order_id", orderID); err != nil {
		return err
	}
	if len(lines) == 0 || len(lines) > validate.MaxCartLines {
		return apperr.Invalid("between 1 and %d lines are required", validate.MaxCartLines)
	}
	for _, l := range lines {
		if err := validate.ID("product_id", l.ProductID); err != nil {
			return err
		}
		if err := validate.Quantity(l.Quantity); err != nil {
			return err
		}
	}
	return s.store.Reserve(ctx, orderID, lines)
}

// Release returns an order's reserved stock.
func (s *Service) Release(ctx context.Context, orderID string) error {
	if err := validate.ID("order_id", orderID); err != nil {
		return err
	}
	return s.store.Release(ctx, orderID)
}

// Commit finalizes an order's reserved stock as sold.
func (s *Service) Commit(ctx context.Context, orderID string) error {
	if err := validate.ID("order_id", orderID); err != nil {
		return err
	}
	return s.store.Commit(ctx, orderID)
}
