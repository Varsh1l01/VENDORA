package main

import (
	"log"
	"net"
	"strings"

	"google.golang.org/grpc"

	"github.com/ayushdevani/forgecart/gen"
	"github.com/ayushdevani/forgecart/pkg/config"
	"github.com/ayushdevani/forgecart/pkg/kafka"
	"github.com/ayushdevani/forgecart/services/payment-service/handler"
	"github.com/ayushdevani/forgecart/services/payment-service/repository"
	"github.com/ayushdevani/forgecart/services/payment-service/service"
)

func main() {
	port := config.GetEnv("PAYMENT_SERVICE_PORT", "50055")
	dbURL := config.GetEnv("PAYMENT_DB_URL", "postgres://forgecart:forgecart_pass@localhost:5432/payments_db?sslmode=disable")
	kafkaBrokers := config.GetEnv("KAFKA_BROKERS", "localhost:9092")
	kafkaTopic := config.GetEnv("KAFKA_TOPIC", "notifications")

	addrs := handler.ServiceAddresses{
		User:  config.GetEnv("USER_SERVICE_ADDR", "localhost:50051"),
		Order: config.GetEnv("ORDER_SERVICE_ADDR", "localhost:50054"),
	}

	repo, err := repository.NewPaymentRepository(dbURL)
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

	svc := service.NewPaymentService(repo)

	h, err := handler.NewPaymentHandler(svc, addrs, producer)
	if err != nil {
		log.Fatalf("Failed to connect to services: %v", err)
	}
	defer h.Close()

	grpcServer := grpc.NewServer()
	gen.RegisterPaymentServiceServer(grpcServer, h)

	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("Payment Service starting on port %s", port)
	log.Printf("Connected to User=%s, Order=%s", addrs.User, addrs.Order)
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
