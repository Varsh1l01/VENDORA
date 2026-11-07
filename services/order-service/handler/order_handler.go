package handler

import (
	"context"
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/pkg/kafka"
	"github.com/ayushdevani/forgecart/services/order-service/repository"
	"github.com/ayushdevani/forgecart/services/order-service/service"
)

type OrderHandler struct {
	gen.UnimplementedOrderServiceServer
	svc             *service.OrderService
	cartConn        *grpc.ClientConn
	cartClient      gen.CartServiceClient
	userConn        *grpc.ClientConn
	userClient      gen.UserServiceClient
	inventoryConn   *grpc.ClientConn
	inventoryClient gen.InventoryServiceClient
	paymentConn     *grpc.ClientConn
	paymentClient   gen.PaymentServiceClient
	producer        *kafka.Producer
}

type ServiceAddresses struct {
	Cart      string
	User      string
	Inventory string
	Payment   string
}

func NewOrderHandler(svc *service.OrderService, addrs ServiceAddresses, producer *kafka.Producer) (*OrderHandler, error) {
	cartConn, err := grpc.NewClient(addrs.Cart, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	userConn, err := grpc.NewClient(addrs.User, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		cartConn.Close()
		return nil, err
	}

	inventoryConn, err := grpc.NewClient(addrs.Inventory, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		cartConn.Close()
		userConn.Close()
		return nil, err
	}

	paymentConn, err := grpc.NewClient(addrs.Payment, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		cartConn.Close()
		userConn.Close()
		inventoryConn.Close()
		return nil, err
	}

	return &OrderHandler{
		svc:             svc,
		cartConn:        cartConn,
		cartClient:      gen.NewCartServiceClient(cartConn),
		userConn:        userConn,
		userClient:      gen.NewUserServiceClient(userConn),
		inventoryConn:   inventoryConn,
		inventoryClient: gen.NewInventoryServiceClient(inventoryConn),
		paymentConn:     paymentConn,
		paymentClient:   gen.NewPaymentServiceClient(paymentConn),
		producer:        producer,
	}, nil
}

func (h *OrderHandler) Close() error {
	if h.cartConn != nil {
		h.cartConn.Close()
	}
	if h.userConn != nil {
		h.userConn.Close()
	}
	if h.inventoryConn != nil {
		h.inventoryConn.Close()
	}
	if h.paymentConn != nil {
		h.paymentConn.Close()
	}
	return nil
}

func (h *OrderHandler) getUserEmail(ctx context.Context, userID string) string {
	user, err := h.userClient.GetUser(ctx, &gen.GetUserRequest{Id: userID})
	if err != nil {
		log.Printf("Failed to fetch user email: %v", err)
		return ""
	}
	return user.Email
}

func (h *OrderHandler) reserveInventory(ctx context.Context, orderID string, items []repository.OrderItem) error {
	for _, item := range items {
		resp, err := h.inventoryClient.ReserveStock(ctx, &gen.ReserveStockRequest{
			OrderId:   orderID,
			ProductId: item.ProductID,
			Quantity:  item.Quantity,
		})
		if err != nil {
			return err
		}
		if !resp.Success {
			return status.Errorf(codes.FailedPrecondition, "insufficient stock for product %s", item.ProductID)
		}
	}
	return nil
}

func (h *OrderHandler) releaseInventory(ctx context.Context, orderID string, items []repository.OrderItem) {
	for _, item := range items {
		_, err := h.inventoryClient.ReleaseStock(ctx, &gen.ReleaseStockRequest{
			OrderId:   orderID,
			ProductId: item.ProductID,
		})
		if err != nil {
			log.Printf("Failed to release stock for product %s: %v", item.ProductID, err)
		}
	}
}

func (h *OrderHandler) createPayment(ctx context.Context, orderID, userID string, amount float64) {
	_, err := h.paymentClient.CreatePayment(ctx, &gen.CreatePaymentRequest{
		OrderId: orderID,
		UserId:  userID,
		Amount:  amount,
	})
	if err != nil {
		log.Printf("Failed to create payment for order %s: %v", orderID, err)
	}
}

func (h *OrderHandler) CreateOrder(ctx context.Context, req *gen.CreateOrderRequest) (*gen.Order, error) {
	// Fetch cart
	cart, err := h.cartClient.GetCart(ctx, &gen.GetCartRequest{UserId: req.UserId})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to fetch cart: "+err.Error())
	}

	if len(cart.Items) == 0 {
		return nil, status.Error(codes.InvalidArgument, "cart is empty")
	}

	// Convert cart items
	var items []repository.OrderItem
	for _, item := range cart.Items {
		items = append(items, repository.OrderItem{
			ProductID:   item.ProductId,
			ProductName: item.Name,
			Price:       item.Price,
			Quantity:    item.Quantity,
		})
	}

	// Create order first to get order ID
	order, err := h.svc.Create(ctx, req.UserId, "", items)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Reserve inventory for all items
	if err := h.reserveInventory(ctx, order.ID, items); err != nil {
		// Rollback: cancel the order
		h.svc.UpdateStatus(ctx, order.ID, "cancelled")
		return nil, status.Error(codes.FailedPrecondition, "failed to reserve inventory: "+err.Error())
	}

	// Clear cart
	_, _ = h.cartClient.ClearCart(ctx, &gen.ClearCartRequest{UserId: req.UserId})

	// Create pending payment
	go h.createPayment(context.Background(), order.ID, order.UserID, order.TotalAmount)

	// Publish event
	if h.producer != nil {
		go func() {
			email := h.getUserEmail(context.Background(), order.UserID)
			h.producer.Publish(context.Background(), "order.created", map[string]interface{}{
				"order_id": order.ID,
				"user_id":  order.UserID,
				"email":    email,
				"total":    order.TotalAmount,
			})
		}()
	}

	return toProtoOrder(order), nil
}

func (h *OrderHandler) GetOrder(ctx context.Context, req *gen.GetOrderRequest) (*gen.Order, error) {
	order, err := h.svc.GetByID(ctx, req.Id)
	if err != nil {
		if err == service.ErrOrderNotFound {
			return nil, status.Error(codes.NotFound, "order not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return toProtoOrder(order), nil
}

func (h *OrderHandler) ListOrders(ctx context.Context, req *gen.ListOrdersRequest) (*gen.OrderList, error) {
	orders, total, err := h.svc.ListByUserID(ctx, req.UserId, req.Page, req.Limit)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	var protoOrders []*gen.Order
	for _, o := range orders {
		protoOrders = append(protoOrders, toProtoOrder(o))
	}

	return &gen.OrderList{
		Orders: protoOrders,
		Total:  total,
	}, nil
}

func (h *OrderHandler) UpdateOrderStatus(ctx context.Context, req *gen.UpdateOrderStatusRequest) (*gen.Order, error) {
	order, err := h.svc.UpdateStatus(ctx, req.Id, req.Status)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// If cancelled, release inventory
	if req.Status == "cancelled" {
		go func() {
			fullOrder, _ := h.svc.GetByID(context.Background(), order.ID)
			if fullOrder != nil {
				var items []repository.OrderItem
				for _, item := range fullOrder.Items {
					items = append(items, item)
				}
				h.releaseInventory(context.Background(), order.ID, items)
			}
		}()
	}

	// Publish event
	if h.producer != nil {
		go func() {
			email := h.getUserEmail(context.Background(), order.UserID)
			h.producer.Publish(context.Background(), "order.status.updated", map[string]interface{}{
				"order_id": order.ID,
				"user_id":  order.UserID,
				"email":    email,
				"status":   order.Status,
			})
		}()
	}

	return toProtoOrder(order), nil
}

func toProtoOrder(o *repository.Order) *gen.Order {
	var items []*gen.OrderItem
	for _, item := range o.Items {
		items = append(items, &gen.OrderItem{
			ProductId: item.ProductID,
			Name:      item.ProductName,
			Price:     item.Price,
			Quantity:  item.Quantity,
		})
	}

	return &gen.Order{
		Id:        o.ID,
		UserId:    o.UserID,
		Status:    o.Status,
		Total:     o.TotalAmount,
		Items:     items,
		CreatedAt: o.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}
