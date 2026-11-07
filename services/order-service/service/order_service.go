package service

import (
	"context"
	"errors"

	"github.com/ayushdevani/forgecart/services/order-service/repository"
)

var (
	ErrOrderNotFound = errors.New("order not found")
)

type OrderService struct {
	repo *repository.OrderRepository
}

func NewOrderService(repo *repository.OrderRepository) *OrderService {
	return &OrderService{repo: repo}
}

func (s *OrderService) Create(ctx context.Context, userID, shippingAddress string, items []repository.OrderItem) (*repository.Order, error) {
	// Calculate total
	var total float64
	for _, item := range items {
		total += item.Price * float64(item.Quantity)
	}

	order := &repository.Order{
		UserID:          userID,
		ShippingAddress: shippingAddress,
		TotalAmount:     total,
		Items:           items,
	}

	if err := s.repo.Create(ctx, order); err != nil {
		return nil, err
	}

	return order, nil
}

func (s *OrderService) GetByID(ctx context.Context, id string) (*repository.Order, error) {
	order, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, ErrOrderNotFound
	}
	return order, nil
}

func (s *OrderService) ListByUserID(ctx context.Context, userID string, page, limit int32) ([]*repository.Order, int32, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 10
	}
	return s.repo.ListByUserID(ctx, userID, page, limit)
}

func (s *OrderService) UpdateStatus(ctx context.Context, id, status string) (*repository.Order, error) {
	if err := s.repo.UpdateStatus(ctx, id, status); err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, id)
}
