// Package handler adapts the inventory service to gRPC.
package handler

import (
	"context"

	inventoryv1 "github.com/Varsh1l01/VENDORA/gen/vendora/inventory/v1"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/grpcx"
	"github.com/Varsh1l01/VENDORA/services/inventory-service/repository"
	"github.com/Varsh1l01/VENDORA/services/inventory-service/service"
)

// Policy is the per-method authorization policy. Reservation RPCs are
// internal: only other services (the order saga) may call them.
var Policy = grpcx.Policy{
	inventoryv1.InventoryService_GetStock_FullMethodName:     grpcx.Public(),
	inventoryv1.InventoryService_SetStock_FullMethodName:     grpcx.Allow(auth.RoleAdmin),
	inventoryv1.InventoryService_AdjustStock_FullMethodName:  grpcx.Allow(auth.RoleAdmin),
	inventoryv1.InventoryService_ReserveStock_FullMethodName: grpcx.Allow(auth.RoleService),
	inventoryv1.InventoryService_ReleaseStock_FullMethodName: grpcx.Allow(auth.RoleService),
}

// Handler implements inventoryv1.InventoryServiceServer.
type Handler struct {
	inventoryv1.UnimplementedInventoryServiceServer
	svc *service.Service
}

// New returns a Handler.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) GetStock(ctx context.Context, req *inventoryv1.GetStockRequest) (*inventoryv1.GetStockResponse, error) {
	s, err := h.svc.Get(ctx, req.GetProductId())
	if err != nil {
		return nil, err
	}
	out := toProto(s)
	// Reservation counts are operational data, visible to staff only.
	if p, ok := auth.FromContext(ctx); !ok || (!p.IsAdmin() && !p.IsService()) {
		out.Reserved = 0
	}
	return &inventoryv1.GetStockResponse{Stock: out}, nil
}

func (h *Handler) SetStock(ctx context.Context, req *inventoryv1.SetStockRequest) (*inventoryv1.SetStockResponse, error) {
	s, err := h.svc.Set(ctx, req.GetProductId(), req.GetAvailable())
	if err != nil {
		return nil, err
	}
	return &inventoryv1.SetStockResponse{Stock: toProto(s)}, nil
}

func (h *Handler) AdjustStock(ctx context.Context, req *inventoryv1.AdjustStockRequest) (*inventoryv1.AdjustStockResponse, error) {
	s, err := h.svc.Adjust(ctx, req.GetProductId(), req.GetDelta())
	if err != nil {
		return nil, err
	}
	return &inventoryv1.AdjustStockResponse{Stock: toProto(s)}, nil
}

func (h *Handler) ReserveStock(ctx context.Context, req *inventoryv1.ReserveStockRequest) (*inventoryv1.ReserveStockResponse, error) {
	lines := make([]repository.Line, 0, len(req.GetLines()))
	for _, l := range req.GetLines() {
		lines = append(lines, repository.Line{ProductID: l.GetProductId(), Quantity: l.GetQuantity()})
	}
	if err := h.svc.Reserve(ctx, req.GetOrderId(), lines); err != nil {
		return nil, err
	}
	return &inventoryv1.ReserveStockResponse{}, nil
}

func (h *Handler) ReleaseStock(ctx context.Context, req *inventoryv1.ReleaseStockRequest) (*inventoryv1.ReleaseStockResponse, error) {
	if err := h.svc.Release(ctx, req.GetOrderId()); err != nil {
		return nil, err
	}
	return &inventoryv1.ReleaseStockResponse{}, nil
}

func toProto(s *repository.Stock) *inventoryv1.Stock {
	return &inventoryv1.Stock{ProductId: s.ProductID, Available: s.Available, Reserved: s.Reserved}
}
