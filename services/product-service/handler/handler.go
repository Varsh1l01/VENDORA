// Package handler adapts the product service to gRPC.
package handler

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/grpcx"
	"github.com/Varsh1l01/VENDORA/services/product-service/repository"
	"github.com/Varsh1l01/VENDORA/services/product-service/service"
)

// Policy is the per-method authorization policy.
var Policy = grpcx.Policy{
	productv1.ProductService_CreateProduct_FullMethodName:    grpcx.Allow(auth.RoleAdmin),
	productv1.ProductService_UpdateProduct_FullMethodName:    grpcx.Allow(auth.RoleAdmin),
	productv1.ProductService_DeleteProduct_FullMethodName:    grpcx.Allow(auth.RoleAdmin),
	productv1.ProductService_GetProduct_FullMethodName:       grpcx.Public(),
	productv1.ProductService_ListProducts_FullMethodName:     grpcx.Public(),
	productv1.ProductService_BatchGetProducts_FullMethodName: grpcx.Public(),
}

// Handler implements productv1.ProductServiceServer.
type Handler struct {
	productv1.UnimplementedProductServiceServer
	svc *service.Service
}

// New returns a Handler.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) CreateProduct(ctx context.Context, req *productv1.CreateProductRequest) (*productv1.CreateProductResponse, error) {
	p, err := h.svc.Create(ctx, service.CreateInput{
		Name: req.GetName(), Description: req.GetDescription(), ImageURL: req.GetImageUrl(),
		PriceCents: req.GetPriceCents(), InitialStock: req.GetInitialStock(),
	})
	if err != nil {
		return nil, err
	}
	return &productv1.CreateProductResponse{Product: ToProto(p)}, nil
}

func (h *Handler) GetProduct(ctx context.Context, req *productv1.GetProductRequest) (*productv1.GetProductResponse, error) {
	p, err := h.svc.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &productv1.GetProductResponse{Product: ToProto(p)}, nil
}

func (h *Handler) BatchGetProducts(ctx context.Context, req *productv1.BatchGetProductsRequest) (*productv1.BatchGetProductsResponse, error) {
	ps, err := h.svc.BatchGet(ctx, req.GetIds())
	if err != nil {
		return nil, err
	}
	out := make([]*productv1.Product, 0, len(ps))
	for _, p := range ps {
		out = append(out, ToProto(p))
	}
	return &productv1.BatchGetProductsResponse{Products: out}, nil
}

func (h *Handler) ListProducts(ctx context.Context, req *productv1.ListProductsRequest) (*productv1.ListProductsResponse, error) {
	ps, next, err := h.svc.List(ctx, req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	out := make([]*productv1.Product, 0, len(ps))
	for _, p := range ps {
		out = append(out, ToProto(p))
	}
	return &productv1.ListProductsResponse{Products: out, NextPageToken: next}, nil
}

func (h *Handler) UpdateProduct(ctx context.Context, req *productv1.UpdateProductRequest) (*productv1.UpdateProductResponse, error) {
	p, err := h.svc.Update(ctx, service.UpdateInput{ID: req.GetId(), Patch: repository.Patch{
		Name: req.Name, Description: req.Description, PriceCents: req.PriceCents,
		ImageURL: req.ImageUrl, IsActive: req.IsActive,
	}})
	if err != nil {
		return nil, err
	}
	return &productv1.UpdateProductResponse{Product: ToProto(p)}, nil
}

func (h *Handler) DeleteProduct(ctx context.Context, req *productv1.DeleteProductRequest) (*productv1.DeleteProductResponse, error) {
	if err := h.svc.Delete(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &productv1.DeleteProductResponse{}, nil
}

// ToProto converts a stored product to its wire form.
func ToProto(p *repository.Product) *productv1.Product {
	return &productv1.Product{
		Id: p.ID, Name: p.Name, Description: p.Description, PriceCents: p.PriceCents,
		Currency: p.Currency, ImageUrl: p.ImageURL, IsActive: p.IsActive,
		CreatedAt: timestamppb.New(p.CreatedAt), UpdatedAt: timestamppb.New(p.UpdatedAt),
	}
}
