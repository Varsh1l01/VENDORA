package handler

import (
	"context"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/services/product-service/repository"
	"github.com/ayushdevani/forgecart/services/product-service/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ProductHandler struct {
	gen.UnimplementedProductServiceServer
	svc *service.ProductService
}

func NewProductHandler(svc *service.ProductService) *ProductHandler {
	return &ProductHandler{svc: svc}
}

func (h *ProductHandler) CreateProduct(ctx context.Context, req *gen.CreateProductRequest) (*gen.Product, error) {
	product, err := h.svc.Create(ctx, req.Name, req.Description, req.Price, req.ImageUrl, req.Stock)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return toProtoProduct(product), nil
}

func (h *ProductHandler) GetProduct(ctx context.Context, req *gen.GetProductRequest) (*gen.Product, error) {
	product, err := h.svc.GetByID(ctx, req.Id)
	if err != nil {
		if err == service.ErrProductNotFound {
			return nil, status.Error(codes.NotFound, "product not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return toProtoProduct(product), nil
}

func (h *ProductHandler) ListProducts(ctx context.Context, req *gen.ListProductsRequest) (*gen.ProductList, error) {
	products, total, err := h.svc.List(ctx, req.Page, req.Limit)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	var protoProducts []*gen.Product
	for _, p := range products {
		protoProducts = append(protoProducts, toProtoProduct(p))
	}

	return &gen.ProductList{
		Products: protoProducts,
		Total:    total,
	}, nil
}

func (h *ProductHandler) UpdateProduct(ctx context.Context, req *gen.UpdateProductRequest) (*gen.Product, error) {
	product, err := h.svc.Update(ctx, req.Id, req.Name, req.Description, req.Price, req.ImageUrl, req.Stock, req.IsActive)
	if err != nil {
		if err == service.ErrProductNotFound {
			return nil, status.Error(codes.NotFound, "product not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return toProtoProduct(product), nil
}

func (h *ProductHandler) DeleteProduct(ctx context.Context, req *gen.DeleteProductRequest) (*gen.DeleteProductResponse, error) {
	err := h.svc.Delete(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &gen.DeleteProductResponse{Success: true}, nil
}

func toProtoProduct(p *repository.Product) *gen.Product {
	return &gen.Product{
		Id:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Price:       p.Price,
		ImageUrl:    p.ImageURL,
		Stock:       p.Stock,
		IsActive:    p.IsActive,
		CreatedAt:   p.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}
