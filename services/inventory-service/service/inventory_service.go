package service

import (
	"context"
	"errors"

	"github.com/ayushdevani/forgecart/services/inventory-service/repository"
)

var (
	ErrStockNotFound     = errors.New("stock not found")
	ErrInsufficientStock = errors.New("insufficient stock")
)

type InventoryService struct {
	repo *repository.InventoryRepository
}

func NewInventoryService(repo *repository.InventoryRepository) *InventoryService {
	return &InventoryService{repo: repo}
}

func (s *InventoryService) GetStock(ctx context.Context, productID string) (*repository.Stock, error) {
	stock, err := s.repo.GetStock(ctx, productID)
	if err != nil {
		return nil, ErrStockNotFound
	}
	return stock, nil
}

func (s *InventoryService) UpdateStock(ctx context.Context, productID string, quantity int32) (*repository.Stock, error) {
	return s.repo.UpdateStock(ctx, productID, quantity)
}

func (s *InventoryService) Reserve(ctx context.Context, orderID, productID string, quantity int32) error {
	stock, err := s.repo.GetStock(ctx, productID)
	if err != nil {
		return ErrStockNotFound
	}

	if stock.Available < quantity {
		return ErrInsufficientStock
	}

	err = s.repo.Reserve(ctx, productID, quantity)
	if err == repository.ErrNoRowsAffected {
		return ErrInsufficientStock
	}
	return err
}

func (s *InventoryService) Release(ctx context.Context, orderID, productID string) error {
	return s.repo.Release(ctx, productID, 1)
}
