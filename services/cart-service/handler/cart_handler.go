package handler

import (
	"context"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/services/cart-service/repository"
	"github.com/ayushdevani/forgecart/services/cart-service/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type CartHandler struct {
	gen.UnimplementedCartServiceServer
	svc *service.CartService
}

func NewCartHandler(svc *service.CartService) *CartHandler {
	return &CartHandler{svc: svc}
}

func (h *CartHandler) GetCart(ctx context.Context, req *gen.GetCartRequest) (*gen.Cart, error) {
	cart, err := h.svc.GetCart(ctx, req.UserId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return toProtoCart(cart), nil
}

func (h *CartHandler) AddItem(ctx context.Context, req *gen.AddItemRequest) (*gen.Cart, error) {
	item := repository.CartItem{
		ProductID: req.ProductId,
		Quantity:  req.Quantity,
	}

	cart, err := h.svc.AddItem(ctx, req.UserId, item)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return toProtoCart(cart), nil
}

func (h *CartHandler) RemoveItem(ctx context.Context, req *gen.RemoveItemRequest) (*gen.Cart, error) {
	cart, err := h.svc.RemoveItem(ctx, req.UserId, req.ProductId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return toProtoCart(cart), nil
}

func (h *CartHandler) ClearCart(ctx context.Context, req *gen.ClearCartRequest) (*gen.ClearCartResponse, error) {
	err := h.svc.ClearCart(ctx, req.UserId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &gen.ClearCartResponse{Success: true}, nil
}

func toProtoCart(c *repository.Cart) *gen.Cart {
	var items []*gen.CartItem
	for _, item := range c.Items {
		items = append(items, &gen.CartItem{
			ProductId: item.ProductID,
			Name:      item.Name,
			Price:     item.Price,
			Quantity:  item.Quantity,
			ImageUrl:  item.ImageURL,
		})
	}

	return &gen.Cart{
		UserId: c.UserID,
		Items:  items,
		Total:  c.Total,
	}
}
