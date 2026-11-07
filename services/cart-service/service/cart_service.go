package service

import (
	"context"

	"github.com/ayushdevani/forgecart/services/cart-service/repository"
)

type CartService struct {
	repo *repository.CartRepository
}

func NewCartService(repo *repository.CartRepository) *CartService {
	return &CartService{repo: repo}
}

func (s *CartService) GetCart(ctx context.Context, userID string) (*repository.Cart, error) {
	return s.repo.GetCart(ctx, userID)
}

func (s *CartService) AddItem(ctx context.Context, userID string, item repository.CartItem) (*repository.Cart, error) {
	cart, err := s.repo.GetCart(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Check if item already exists
	found := false
	for i, existing := range cart.Items {
		if existing.ProductID == item.ProductID {
			cart.Items[i].Quantity += item.Quantity
			found = true
			break
		}
	}

	if !found {
		cart.Items = append(cart.Items, item)
	}

	if err := s.repo.SaveCart(ctx, cart); err != nil {
		return nil, err
	}

	return s.repo.GetCart(ctx, userID)
}

func (s *CartService) RemoveItem(ctx context.Context, userID, productID string) (*repository.Cart, error) {
	cart, err := s.repo.GetCart(ctx, userID)
	if err != nil {
		return nil, err
	}

	var newItems []repository.CartItem
	for _, item := range cart.Items {
		if item.ProductID != productID {
			newItems = append(newItems, item)
		}
	}
	cart.Items = newItems

	if err := s.repo.SaveCart(ctx, cart); err != nil {
		return nil, err
	}

	return s.repo.GetCart(ctx, userID)
}

func (s *CartService) ClearCart(ctx context.Context, userID string) error {
	return s.repo.ClearCart(ctx, userID)
}
