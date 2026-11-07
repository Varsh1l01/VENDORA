package service

import (
	"context"
	"errors"

	"github.com/ayushdevani/forgecart/services/payment-service/repository"
)

var (
	ErrPaymentNotFound = errors.New("payment not found")
)

type PaymentService struct {
	repo *repository.PaymentRepository
}

func NewPaymentService(repo *repository.PaymentRepository) *PaymentService {
	return &PaymentService{repo: repo}
}

func (s *PaymentService) Create(ctx context.Context, orderID, userID string, amount float64) (*repository.Payment, error) {
	payment := &repository.Payment{
		OrderID: orderID,
		UserID:  userID,
		Amount:  amount,
	}

	if err := s.repo.Create(ctx, payment); err != nil {
		return nil, err
	}

	return payment, nil
}

func (s *PaymentService) GetByOrderID(ctx context.Context, orderID string) (*repository.Payment, error) {
	payment, err := s.repo.GetByOrderID(ctx, orderID)
	if err != nil {
		return nil, ErrPaymentNotFound
	}
	return payment, nil
}

func (s *PaymentService) UpdateStatus(ctx context.Context, id, status string) (*repository.Payment, error) {
	if err := s.repo.UpdateStatus(ctx, id, status); err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, id)
}
