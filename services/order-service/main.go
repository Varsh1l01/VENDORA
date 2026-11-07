package main

import (
	"log"
	"net"
	"strings"

	"google.golang.org/grpc"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/pkg/config"
	"github.com/ayushdevani/forgecart/pkg/kafka"
	"github.com/ayushdevani/forgecart/services/order-service/handler"
	"github.com/ayushdevani/forgecart/services/order-service/repository"
	"github.com/ayushdevani/forgecart/services/order-service/service"
)

func main() {
	port := config.GetEnv("ORDER_SERVICE_PORT", "50054")
	dbURL := config.GetEnv("ORDER_DB_URL", "postgres://forgecart:forgecart_pass@localhost:5432/orders_db?sslmode=disable")
	kafkaBrokers := config.GetEnv("KAFKA_BROKERS", "localhost:9092")
	kafkaTopic := config.GetEnv("KAFKA_TOPIC", "notifications")

	addrs := handler.ServiceAddresses{
		Cart:      config.GetEnv("CART_SERVICE_ADDR", "localhost:50053"),
		User:      config.GetEnv("USER_SERVICE_ADDR", "localhost:50051"),
		Inventory: config.GetEnv("INVENTORY_SERVICE_ADDR", "localhost:50056"),
		Payment:   config.GetEnv("PAYMENT_SERVICE_ADDR", "localhost:50055"),
	}

	repo, err := repository.NewOrderRepository(dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer repo.Close()

	var producer *kafka.Producer
	if kafkaBrokers != "" {
		producer = kafka.NewProducer(strings.Split(kafkaBrokers, ","), kafkaTopic)
		defer producer.Close()
		log.Printf("Kafka producer connected to %s", kafkaBrokers)
	}

	svc := service.NewOrderService(repo)

	h, err := handler.NewOrderHandler(svc, addrs, producer)
	if err != nil {
		log.Fatalf("Failed to connect to services: %v", err)
	}
	defer h.Close()

	grpcServer := grpc.NewServer()
	gen.RegisterOrderServiceServer(grpcServer, h)

	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("Order Service starting on port %s", port)
	log.Printf("Connected to: Cart=%s, User=%s, Inventory=%s, Payment=%s",
		addrs.Cart, addrs.User, addrs.Inventory, addrs.Payment)
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
