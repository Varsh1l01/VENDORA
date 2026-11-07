package handler

import (
	"context"
	"log"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/pkg/kafka"
	"github.com/ayushdevani/forgecart/services/payment-service/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type PaymentHandler struct {
	gen.UnimplementedPaymentServiceServer
	svc         *service.PaymentService
	producer    *kafka.Producer
	userConn    *grpc.ClientConn
	userClient  gen.UserServiceClient
	orderConn   *grpc.ClientConn
	orderClient gen.OrderServiceClient
}

type ServiceAddresses struct {
	User  string
	Order string
}

func NewPaymentHandler(svc *service.PaymentService, addrs ServiceAddresses, producer *kafka.Producer) (*PaymentHandler, error) {
	userConn, err := grpc.NewClient(addrs.User, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	orderConn, err := grpc.NewClient(addrs.Order, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		userConn.Close()
		return nil, err
	}

	return &PaymentHandler{
		svc:         svc,
		producer:    producer,
		userConn:    userConn,
		userClient:  gen.NewUserServiceClient(userConn),
		orderConn:   orderConn,
		orderClient: gen.NewOrderServiceClient(orderConn),
	}, nil
}

func (h *PaymentHandler) Close() error {
	if h.userConn != nil {
		h.userConn.Close()
	}
	if h.orderConn != nil {
		h.orderConn.Close()
	}
	return nil
}

func (h *PaymentHandler) getUserEmail(ctx context.Context, userID string) string {
	user, err := h.userClient.GetUser(ctx, &gen.GetUserRequest{Id: userID})
	if err != nil {
		log.Printf("Failed to fetch user email: %v", err)
		return ""
	}
	return user.Email
}

func (h *PaymentHandler) confirmOrder(ctx context.Context, orderID string) {
	_, err := h.orderClient.UpdateOrderStatus(ctx, &gen.UpdateOrderStatusRequest{
		Id:     orderID,
		Status: "confirmed",
	})
	if err != nil {
		log.Printf("Failed to confirm order %s: %v", orderID, err)
	}
}

func (h *PaymentHandler) CreatePayment(ctx context.Context, req *gen.CreatePaymentRequest) (*gen.Payment, error) {
	payment, err := h.svc.Create(ctx, req.OrderId, req.UserId, req.Amount)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &gen.Payment{
		Id:        payment.ID,
		OrderId:   payment.OrderID,
		UserId:    payment.UserID,
		Amount:    payment.Amount,
		Status:    payment.Status,
		CreatedAt: payment.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}, nil
}

func (h *PaymentHandler) GetPayment(ctx context.Context, req *gen.GetPaymentRequest) (*gen.Payment, error) {
	payment, err := h.svc.GetByOrderID(ctx, req.OrderId)
	if err != nil {
		if err == service.ErrPaymentNotFound {
			return nil, status.Error(codes.NotFound, "payment not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &gen.Payment{
		Id:        payment.ID,
		OrderId:   payment.OrderID,
		UserId:    payment.UserID,
		Amount:    payment.Amount,
		Status:    payment.Status,
		CreatedAt: payment.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}, nil
}

func (h *PaymentHandler) UpdatePaymentStatus(ctx context.Context, req *gen.UpdatePaymentStatusRequest) (*gen.Payment, error) {
	payment, err := h.svc.UpdateStatus(ctx, req.Id, req.Status)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// When payment completed, confirm the order
	if req.Status == "completed" {
		go h.confirmOrder(context.Background(), payment.OrderID)

		// Publish payment.completed event
		if h.producer != nil {
			go func() {
				email := h.getUserEmail(context.Background(), payment.UserID)
				h.producer.Publish(context.Background(), "payment.completed", map[string]interface{}{
					"payment_id": payment.ID,
					"order_id":   payment.OrderID,
					"user_id":    payment.UserID,
					"email":      email,
					"amount":     payment.Amount,
				})
			}()
		}
	}

	return &gen.Payment{
		Id:        payment.ID,
		OrderId:   payment.OrderID,
		UserId:    payment.UserID,
		Amount:    payment.Amount,
		Status:    payment.Status,
		CreatedAt: payment.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}, nil
}
