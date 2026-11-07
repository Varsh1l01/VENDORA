package service

import (
	"context"
	"errors"

	"github.com/ayushdevani/forgecart/services/product-service/repository"
)

var (
	ErrProductNotFound = errors.New("product not found")
)

type ProductService struct {
	repo *repository.ProductRepository
}

func NewProductService(repo *repository.ProductRepository) *ProductService {
	return &ProductService{repo: repo}
}

func (s *ProductService) Create(ctx context.Context, name, description string, price float64, imageURL string, stock int32) (*repository.Product, error) {
	product := &repository.Product{
		Name:        name,
		Description: description,
		Price:       price,
		ImageURL:    imageURL,
		Stock:       stock,
		IsActive:    true,
	}

	if err := s.repo.Create(ctx, product); err != nil {
		return nil, err
	}

	return product, nil
}

func (s *ProductService) GetByID(ctx context.Context, id string) (*repository.Product, error) {
	product, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, ErrProductNotFound
	}
	return product, nil
}

func (s *ProductService) List(ctx context.Context, page, limit int32) ([]*repository.Product, int32, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 10
	}
	return s.repo.List(ctx, page, limit)
}

func (s *ProductService) Update(ctx context.Context, id, name, description string, price float64, imageURL string, stock int32, isActive bool) (*repository.Product, error) {
	product, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, ErrProductNotFound
	}

	product.Name = name
	product.Description = description
	product.Price = price
	product.ImageURL = imageURL
	product.Stock = stock
	product.IsActive = isActive

	if err := s.repo.Update(ctx, product); err != nil {
		return nil, err
	}

	return product, nil
}

func (s *ProductService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}
