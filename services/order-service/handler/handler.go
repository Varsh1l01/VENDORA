// Package handler adapts the order service to gRPC and wraps downstream
// gRPC clients behind the service's ports.
package handler

import (
	"context"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	cartv1 "github.com/Varsh1l01/VENDORA/gen/vendora/cart/v1"
	inventoryv1 "github.com/Varsh1l01/VENDORA/gen/vendora/inventory/v1"
	orderv1 "github.com/Varsh1l01/VENDORA/gen/vendora/order/v1"
	productv1 "github.com/Varsh1l01/VENDORA/gen/vendora/product/v1"
	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/grpcx"
	"github.com/Varsh1l01/VENDORA/services/order-service/repository"
	"github.com/Varsh1l01/VENDORA/services/order-service/service"
)

var anyCaller = grpcx.Allow(auth.RoleCustomer, auth.RoleAdmin, auth.RoleService)

// Policy is the per-method authorization policy.
var Policy = grpcx.Policy{
	orderv1.OrderService_CreateOrder_FullMethodName:       anyCaller,
	orderv1.OrderService_GetOrder_FullMethodName:          anyCaller,
	orderv1.OrderService_ListOrders_FullMethodName:        anyCaller,
	orderv1.OrderService_CancelOrder_FullMethodName:       anyCaller,
	orderv1.OrderService_UpdateOrderStatus_FullMethodName: grpcx.Allow(auth.RoleAdmin),
}

// Handler implements orderv1.OrderServiceServer.
type Handler struct {
	orderv1.UnimplementedOrderServiceServer
	svc *service.Service
}

// New returns a Handler.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) CreateOrder(ctx context.Context, req *orderv1.CreateOrderRequest) (*orderv1.CreateOrderResponse, error) {
	o, err := h.svc.Create(ctx, service.CreateInput{
		UserID: req.GetUserId(), IdempotencyKey: req.GetIdempotencyKey(), ShippingAddress: req.GetShippingAddress(),
	})
	if err != nil {
		return nil, err
	}
	return &orderv1.CreateOrderResponse{Order: ToProto(o)}, nil
}

func (h *Handler) GetOrder(ctx context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
	o, err := h.svc.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	// A service acting for a user (e.g. payment-service) passes user_id:
	// enforce it so services cannot be used to read other users' orders.
	if uid := req.GetUserId(); uid != "" && uid != o.UserID {
		return nil, apperr.NotFound("order not found")
	}
	return &orderv1.GetOrderResponse{Order: ToProto(o)}, nil
}

func (h *Handler) ListOrders(ctx context.Context, req *orderv1.ListOrdersRequest) (*orderv1.ListOrdersResponse, error) {
	os, next, err := h.svc.List(ctx, req.GetUserId(), req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	out := make([]*orderv1.Order, 0, len(os))
	for _, o := range os {
		out = append(out, ToProto(o))
	}
	return &orderv1.ListOrdersResponse{Orders: out, NextPageToken: next}, nil
}

func (h *Handler) CancelOrder(ctx context.Context, req *orderv1.CancelOrderRequest) (*orderv1.CancelOrderResponse, error) {
	o, err := h.svc.Cancel(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &orderv1.CancelOrderResponse{Order: ToProto(o)}, nil
}

func (h *Handler) UpdateOrderStatus(ctx context.Context, req *orderv1.UpdateOrderStatusRequest) (*orderv1.UpdateOrderStatusResponse, error) {
	st, err := service.ParseStatus(strings.TrimPrefix(req.GetStatus().String(), "ORDER_STATUS_"))
	if err != nil {
		return nil, err
	}
	o, err := h.svc.UpdateStatus(ctx, req.GetId(), st)
	if err != nil {
		return nil, err
	}
	return &orderv1.UpdateOrderStatusResponse{Order: ToProto(o)}, nil
}

// ToProto converts a stored order to its wire form.
func ToProto(o *repository.Order) *orderv1.Order {
	out := &orderv1.Order{
		Id: o.ID, UserId: o.UserID, TotalCents: o.TotalCents, Currency: o.Currency,
		ShippingAddress: o.ShippingAddress, StatusReason: o.StatusReason,
		Status:    orderv1.OrderStatus(orderv1.OrderStatus_value["ORDER_STATUS_"+o.Status]),
		CreatedAt: timestamppb.New(o.CreatedAt), UpdatedAt: timestamppb.New(o.UpdatedAt),
	}
	for _, it := range o.Items {
		out.Items = append(out.Items, &orderv1.OrderItem{
			ProductId: it.ProductID, Name: it.Name, UnitPriceCents: it.UnitPriceCents,
			Quantity: it.Quantity, LineTotalCents: it.UnitPriceCents * int64(it.Quantity),
		})
	}
	return out
}

// CartClient adapts cartv1.CartServiceClient.
type CartClient struct{ C cartv1.CartServiceClient }

func (c CartClient) GetCart(ctx context.Context, userID string) (*cartv1.Cart, error) {
	resp, err := c.C.GetCart(ctx, &cartv1.GetCartRequest{UserId: userID})
	if err != nil {
		return nil, apperr.FromStatus(err, "cart.GetCart")
	}
	return resp.GetCart(), nil
}

// CatalogClient adapts productv1.ProductServiceClient.
type CatalogClient struct {
	C productv1.ProductServiceClient
}

func (c CatalogClient) BatchGetProducts(ctx context.Context, ids []string) ([]*productv1.Product, error) {
	resp, err := c.C.BatchGetProducts(ctx, &productv1.BatchGetProductsRequest{Ids: ids})
	if err != nil {
		return nil, apperr.FromStatus(err, "product.BatchGetProducts")
	}
	return resp.GetProducts(), nil
}

// InventoryClient adapts inventoryv1.InventoryServiceClient.
type InventoryClient struct {
	C inventoryv1.InventoryServiceClient
}

func (c InventoryClient) Reserve(ctx context.Context, orderID string, lines []service.Line) error {
	req := &inventoryv1.ReserveStockRequest{OrderId: orderID}
	for _, l := range lines {
		req.Lines = append(req.Lines, &inventoryv1.ReservationLine{ProductId: l.ProductID, Quantity: l.Quantity})
	}
	_, err := c.C.ReserveStock(ctx, req)
	return apperr.FromStatus(err, "inventory.ReserveStock")
}

func (c InventoryClient) Release(ctx context.Context, orderID string) error {
	_, err := c.C.ReleaseStock(ctx, &inventoryv1.ReleaseStockRequest{OrderId: orderID})
	return apperr.FromStatus(err, "inventory.ReleaseStock")
}
