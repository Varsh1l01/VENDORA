// Package handler adapts the payment service to gRPC.
package handler

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "github.com/Varsh1l01/VENDORA/gen/vendora/order/v1"
	paymentv1 "github.com/Varsh1l01/VENDORA/gen/vendora/payment/v1"
	"github.com/Varsh1l01/VENDORA/pkg/apperr"
	"github.com/Varsh1l01/VENDORA/pkg/auth"
	"github.com/Varsh1l01/VENDORA/pkg/grpcx"
	"github.com/Varsh1l01/VENDORA/services/payment-service/repository"
	"github.com/Varsh1l01/VENDORA/services/payment-service/service"
)

var anyCaller = grpcx.Allow(auth.RoleCustomer, auth.RoleAdmin, auth.RoleService)

// Policy is the per-method authorization policy.
var Policy = grpcx.Policy{
	paymentv1.PaymentService_PayOrder_FullMethodName:          anyCaller,
	paymentv1.PaymentService_GetPaymentByOrder_FullMethodName: anyCaller,
}

// Handler implements paymentv1.PaymentServiceServer.
type Handler struct {
	paymentv1.UnimplementedPaymentServiceServer
	svc *service.Service
}

// New returns a Handler.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) PayOrder(ctx context.Context, req *paymentv1.PayOrderRequest) (*paymentv1.PayOrderResponse, error) {
	p, err := h.svc.Pay(ctx, req.GetOrderId(), req.GetUserId(), req.GetPaymentMethodToken())
	if err != nil {
		return nil, err
	}
	return &paymentv1.PayOrderResponse{Payment: toProto(p)}, nil
}

func (h *Handler) GetPaymentByOrder(ctx context.Context, req *paymentv1.GetPaymentByOrderRequest) (*paymentv1.GetPaymentByOrderResponse, error) {
	p, err := h.svc.GetByOrder(ctx, req.GetOrderId())
	if err != nil {
		return nil, err
	}
	return &paymentv1.GetPaymentByOrderResponse{Payment: toProto(p)}, nil
}

func toProto(p *repository.Payment) *paymentv1.Payment {
	return &paymentv1.Payment{
		Id: p.ID, OrderId: p.OrderID, UserId: p.UserID, AmountCents: p.AmountCents, Currency: p.Currency,
		Status:        paymentv1.PaymentStatus(paymentv1.PaymentStatus_value["PAYMENT_STATUS_"+p.Status]),
		FailureReason: p.FailureReason, ProviderReference: p.ProviderReference,
		CreatedAt: timestamppb.New(p.CreatedAt), UpdatedAt: timestamppb.New(p.UpdatedAt),
	}
}

// OrderClient adapts orderv1.OrderServiceClient.
type OrderClient struct{ C orderv1.OrderServiceClient }

// GetOrder fetches an order as this service, scoped to userID.
func (c OrderClient) GetOrder(ctx context.Context, orderID, userID string) (*orderv1.Order, error) {
	resp, err := c.C.GetOrder(ctx, &orderv1.GetOrderRequest{Id: orderID, UserId: userID})
	if err != nil {
		return nil, apperr.FromStatus(err, "order.GetOrder")
	}
	return resp.GetOrder(), nil
}
