package handler

import (
	"context"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/services/inventory-service/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type InventoryHandler struct {
	gen.UnimplementedInventoryServiceServer
	svc *service.InventoryService
}

func NewInventoryHandler(svc *service.InventoryService) *InventoryHandler {
	return &InventoryHandler{svc: svc}
}

func (h *InventoryHandler) GetStock(ctx context.Context, req *gen.GetStockRequest) (*gen.Stock, error) {
	stock, err := h.svc.GetStock(ctx, req.ProductId)
	if err != nil {
		if err == service.ErrStockNotFound {
			return nil, status.Error(codes.NotFound, "stock not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &gen.Stock{
		ProductId: stock.ProductID,
		Available: stock.Available,
		Reserved:  stock.Reserved,
	}, nil
}

func (h *InventoryHandler) UpdateStock(ctx context.Context, req *gen.UpdateStockRequest) (*gen.Stock, error) {
	stock, err := h.svc.UpdateStock(ctx, req.ProductId, req.Quantity)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &gen.Stock{
		ProductId: stock.ProductID,
		Available: stock.Available,
		Reserved:  stock.Reserved,
	}, nil
}

func (h *InventoryHandler) ReserveStock(ctx context.Context, req *gen.ReserveStockRequest) (*gen.ReserveStockResponse, error) {
	err := h.svc.Reserve(ctx, req.OrderId, req.ProductId, req.Quantity)
	if err != nil {
		if err == service.ErrInsufficientStock {
			return &gen.ReserveStockResponse{Success: false, Message: "insufficient stock"}, nil
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &gen.ReserveStockResponse{Success: true, Message: "stock reserved"}, nil
}

func (h *InventoryHandler) ReleaseStock(ctx context.Context, req *gen.ReleaseStockRequest) (*gen.ReleaseStockResponse, error) {
	err := h.svc.Release(ctx, req.OrderId, req.ProductId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &gen.ReleaseStockResponse{Success: true}, nil
}
