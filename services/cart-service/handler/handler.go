// Package handler adapts the cart service to gRPC.
package handler

import (
	"context"

	cartv1 "github.com/Varsh1l01/VENDORA/gen/vendora/cart/v1"
	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/grpcx"
	"github.com/Varsh1l01/VENDORA/services/cart-service/service"
)

var anyCaller = grpcx.Allow(auth.RoleCustomer, auth.RoleAdmin, auth.RoleService)

// Policy is the per-method authorization policy. Ownership is enforced in
// the service via auth.ResolveUser.
var Policy = grpcx.Policy{
	cartv1.CartService_GetCart_FullMethodName:         anyCaller,
	cartv1.CartService_AddItem_FullMethodName:         anyCaller,
	cartv1.CartService_SetItemQuantity_FullMethodName: anyCaller,
	cartv1.CartService_RemoveItem_FullMethodName:      anyCaller,
	cartv1.CartService_ClearCart_FullMethodName:       anyCaller,
}

// Handler implements cartv1.CartServiceServer.
type Handler struct {
	cartv1.UnimplementedCartServiceServer
	svc *service.Service
}

// New returns a Handler.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) GetCart(ctx context.Context, req *cartv1.GetCartRequest) (*cartv1.GetCartResponse, error) {
	c, err := h.svc.Get(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &cartv1.GetCartResponse{Cart: toProto(c)}, nil
}

func (h *Handler) AddItem(ctx context.Context, req *cartv1.AddItemRequest) (*cartv1.AddItemResponse, error) {
	c, err := h.svc.Add(ctx, req.GetUserId(), req.GetProductId(), req.GetQuantity())
	if err != nil {
		return nil, err
	}
	return &cartv1.AddItemResponse{Cart: toProto(c)}, nil
}

func (h *Handler) SetItemQuantity(ctx context.Context, req *cartv1.SetItemQuantityRequest) (*cartv1.SetItemQuantityResponse, error) {
	c, err := h.svc.SetQuantity(ctx, req.GetUserId(), req.GetProductId(), req.GetQuantity())
	if err != nil {
		return nil, err
	}
	return &cartv1.SetItemQuantityResponse{Cart: toProto(c)}, nil
}

func (h *Handler) RemoveItem(ctx context.Context, req *cartv1.RemoveItemRequest) (*cartv1.RemoveItemResponse, error) {
	c, err := h.svc.Remove(ctx, req.GetUserId(), req.GetProductId())
	if err != nil {
		return nil, err
	}
	return &cartv1.RemoveItemResponse{Cart: toProto(c)}, nil
}

func (h *Handler) ClearCart(ctx context.Context, req *cartv1.ClearCartRequest) (*cartv1.ClearCartResponse, error) {
	if err := h.svc.Clear(ctx, req.GetUserId()); err != nil {
		return nil, err
	}
	return &cartv1.ClearCartResponse{}, nil
}

func toProto(c *service.Cart) *cartv1.Cart {
	out := &cartv1.Cart{UserId: c.UserID, TotalCents: c.TotalCents, Currency: c.Currency}
	for _, it := range c.Items {
		out.Items = append(out.Items, &cartv1.CartItem{
			ProductId: it.ProductID, Name: it.Name, UnitPriceCents: it.UnitPriceCents, Quantity: it.Quantity,
			LineTotalCents: it.LineTotalCents, ImageUrl: it.ImageURL, Available: it.Available,
		})
	}
	return out
}

// Catalog adapts the generated ProductService client to service.Catalog.
type Catalog struct {
	Client productv1.ProductServiceClient
}

func (c Catalog) GetProduct(ctx context.Context, id string) (*productv1.Product, error) {
	resp, err := c.Client.GetProduct(ctx, &productv1.GetProductRequest{Id: id})
	if err != nil {
		return nil, apperr.FromStatus(err, "product.GetProduct")
	}
	return resp.GetProduct(), nil
}

func (c Catalog) BatchGetProducts(ctx context.Context, ids []string) ([]*productv1.Product, error) {
	resp, err := c.Client.BatchGetProducts(ctx, &productv1.BatchGetProductsRequest{Ids: ids})
	if err != nil {
		return nil, apperr.FromStatus(err, "product.BatchGetProducts")
	}
	return resp.GetProducts(), nil
}
